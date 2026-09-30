package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	bolt "go.etcd.io/bbolt"
)

// The events bucket is a ring buffer: keys are big-endian uint64 sequence
// numbers taken from the bucket's own persisted sequence counter (stored in
// the bucket header, so it survives restarts and is updated in the same
// transaction as the event), values are JSON Event documents. Every append
// deletes the entries older than the newest maxEvents (MaxEvents = 5000) in
// the same transaction.

// maxImportLine bounds one NDJSON line in ImportEvents.
const maxImportLine = 1 << 20

// AppendEvent stores e with the next sequence number and returns it as
// stored (Seq assigned, At set to now when zero, UTC). An empty Level
// becomes "info"; secrets in Message and Reason are masked.
func (s *Store) AppendEvent(e Event) (Event, error) {
	if e.At.IsZero() {
		e.At = s.now()
	}
	e.At = e.At.UTC()
	normalizeEvent(&e)
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := bucket(tx, BucketEvents)
		if err != nil {
			return err
		}
		seq, err := b.NextSequence()
		if err != nil {
			return err
		}
		e.Seq = seq
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err := b.Put(seqKey(seq), data); err != nil {
			return err
		}
		return pruneBefore(b, seq, s.maxEvents)
	})
	if err != nil {
		return Event{}, s.fail("append event", err)
	}
	return e, nil
}

// Events returns the events matching f, newest first. Tunnel matches
// Event.Tunnel; Node matches Node, FromNode or ToNode; Types matches any of
// the listed types; Since keeps events at or after that instant; Limit keeps
// the newest N after filtering (0 = all).
func (s *Store) Events(f EventFilter) ([]Event, error) {
	out := []Event{}
	err := s.db.View(func(tx *bolt.Tx) error {
		b, err := bucket(tx, BucketEvents)
		if err != nil {
			return err
		}
		c := b.Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			if v == nil {
				continue
			}
			var e Event
			if err := json.Unmarshal(v, &e); err != nil {
				return fmt.Errorf("events/%x: %w", k, err)
			}
			if !f.match(e) {
				continue
			}
			out = append(out, e)
			if f.Limit > 0 && len(out) >= f.Limit {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, s.fail("read events", err)
	}
	return out, nil
}

// normalizeEvent defaults the level and masks secrets in the free text.
func normalizeEvent(e *Event) {
	if e.Level == "" {
		e.Level = LevelInfo
	}
	e.Message = clean(e.Message)
	e.Reason = clean(e.Reason)
}

func (f EventFilter) match(e Event) bool {
	if f.Tunnel != "" && e.Tunnel != f.Tunnel {
		return false
	}
	if f.Node != "" && e.Node != f.Node && e.FromNode != f.Node && e.ToNode != f.Node {
		return false
	}
	if len(f.Types) > 0 && !slices.Contains(f.Types, e.Type) {
		return false
	}
	if !f.Since.IsZero() && e.At.Before(f.Since) {
		return false
	}
	return true
}

// ExportEvents writes every stored event as NDJSON (one JSON document per
// line), oldest first. The read transaction is released before w is
// written, so a slow writer never holds the database.
func (s *Store) ExportEvents(w io.Writer) error {
	var lines [][]byte
	err := s.db.View(func(tx *bolt.Tx) error {
		b, err := bucket(tx, BucketEvents)
		if err != nil {
			return err
		}
		return b.ForEach(func(_, v []byte) error {
			if v != nil {
				lines = append(lines, bytes.Clone(v))
			}
			return nil
		})
	})
	if err != nil {
		return s.fail("export events", err)
	}
	bw := bufio.NewWriter(w)
	for _, l := range lines {
		if _, err := bw.Write(l); err != nil {
			return s.fail("export events", err)
		}
		if err := bw.WriteByte('\n'); err != nil {
			return s.fail("export events", err)
		}
	}
	if err := bw.Flush(); err != nil {
		return s.fail("export events", err)
	}
	return nil
}

// ImportEvents reads NDJSON produced by ExportEvents (restore). Events keep
// their sequence numbers (an event without one gets the next free number);
// an existing event with the same number is replaced. The sequence counter
// moves past the highest imported number and the ring is trimmed to the
// newest MaxEvents. Blank lines are ignored; a malformed line aborts the
// import without changing anything. It returns the number of events read.
func (s *Store) ImportEvents(r io.Reader) (int, error) {
	var events []Event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxImportLine)
	line := 0
	for sc.Scan() {
		line++
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(raw, &e); err != nil {
			return 0, s.fail("import events", fmt.Errorf("line %d: %w", line, err))
		}
		e.At = utc(e.At)
		normalizeEvent(&e)
		events = append(events, e)
	}
	if err := sc.Err(); err != nil {
		return 0, s.fail("import events", err)
	}
	if len(events) == 0 {
		return 0, nil
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := bucket(tx, BucketEvents)
		if err != nil {
			return err
		}
		maxSeq := b.Sequence()
		for _, e := range events {
			if e.Seq > maxSeq {
				maxSeq = e.Seq
			}
		}
		for i := range events {
			if events[i].Seq == 0 {
				maxSeq++
				events[i].Seq = maxSeq
			}
			data, err := json.Marshal(events[i])
			if err != nil {
				return err
			}
			if err := b.Put(seqKey(events[i].Seq), data); err != nil {
				return err
			}
		}
		if err := b.SetSequence(maxSeq); err != nil {
			return err
		}
		return trimToNewest(b, s.maxEvents)
	})
	if err != nil {
		return 0, s.fail("import events", err)
	}
	return len(events), nil
}

// trimToNewest deletes the oldest plain entries until at most keep remain
// (used after an import, when sequence numbers may have gaps).
func trimToNewest(b *bolt.Bucket, keep int) error {
	n := 0
	c := b.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		if v != nil {
			n++
		}
	}
	excess := n - keep
	if excess <= 0 {
		return nil
	}
	victims := make([][]byte, 0, excess)
	for k, v := c.First(); k != nil && len(victims) < excess; k, v = c.Next() {
		if v != nil {
			victims = append(victims, bytes.Clone(k))
		}
	}
	for _, k := range victims {
		if err := b.Delete(k); err != nil {
			return err
		}
	}
	return nil
}
