package state

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// Probe histories live in nested buckets: probes/<tunnel>/<node>/<transport>
// is a bucket whose keys are big-endian sequence numbers (the nested
// bucket's own sequence) and whose values are JSON ProbeSample documents.
// Only the newest maxProbes (MaxProbeSamples = 120) entries are kept, so
// every history is bounded (section 12 budget).

// AppendProbe records one probe sample for (tunnel, node, transport) and
// drops the oldest samples beyond the last 120. A zero At is set to now;
// times are stored in UTC and secrets in Error are masked.
func (s *Store) AppendProbe(tunnel, node, transport string, p ProbeSample) error {
	if tunnel == "" || node == "" || transport == "" {
		return s.fail("append probe", errEmptyKey)
	}
	if p.At.IsZero() {
		p.At = s.now()
	}
	p.At = p.At.UTC()
	p.Error = clean(p.Error)
	data, err := json.Marshal(p)
	if err != nil {
		return s.fail("append probe", err)
	}
	key := Key(tunnel, node, transport)
	err = s.db.Update(func(tx *bolt.Tx) error {
		root, err := bucket(tx, BucketProbes)
		if err != nil {
			return err
		}
		b, err := root.CreateBucketIfNotExists([]byte(key))
		if err != nil {
			return err
		}
		seq, err := b.NextSequence()
		if err != nil {
			return err
		}
		if err := b.Put(seqKey(seq), data); err != nil {
			return err
		}
		return pruneBefore(b, seq, s.maxProbes)
	})
	if err != nil {
		return s.fail("append probe", fmt.Errorf("probes/%s: %w", key, err))
	}
	return nil
}

// Probes returns the stored samples for (tunnel, node, transport), oldest
// first (chronological, as the dashboard chart draws them). An unknown key
// yields an empty slice.
func (s *Store) Probes(tunnel, node, transport string) ([]ProbeSample, error) {
	out := []ProbeSample{}
	key := Key(tunnel, node, transport)
	err := s.db.View(func(tx *bolt.Tx) error {
		root, err := bucket(tx, BucketProbes)
		if err != nil {
			return err
		}
		b := root.Bucket([]byte(key))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, v []byte) error {
			var p ProbeSample
			if err := json.Unmarshal(v, &p); err != nil {
				return err
			}
			out = append(out, p)
			return nil
		})
	})
	if err != nil {
		return nil, s.fail("read probes", fmt.Errorf("probes/%s: %w", key, err))
	}
	return out, nil
}

// ProbeKeys lists every "<tunnel>/<node>/<transport>" that has a history.
func (s *Store) ProbeKeys() ([]string, error) {
	out := []string{}
	err := s.db.View(func(tx *bolt.Tx) error {
		root, err := bucket(tx, BucketProbes)
		if err != nil {
			return err
		}
		return root.ForEach(func(k, v []byte) error {
			if v == nil {
				out = append(out, string(k))
			}
			return nil
		})
	})
	if err != nil {
		return nil, s.fail("list probes", err)
	}
	return out, nil
}

// seqKey encodes a sequence number as an 8-byte big-endian key so that
// bbolt's byte ordering equals numeric ordering.
func seqKey(seq uint64) []byte {
	var k [8]byte
	binary.BigEndian.PutUint64(k[:], seq)
	return k[:]
}

func keySeq(k []byte) (uint64, bool) {
	if len(k) != 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(k), true
}

// pruneBefore deletes every entry that sorts before the first sequence key
// in (last-keep, last]. Sequence keys are unique and never exceed last, so at
// most keep ring entries remain.
func pruneBefore(b *bolt.Bucket, last uint64, keep int) error {
	if keep < 1 {
		keep = 1
	}
	keepN := uint64(keep) //nolint:gosec // keep >= 1 is enforced above
	if last <= keepN {
		return nil
	}
	cutoff := last - keepN
	var victims [][]byte
	c := b.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		seq, ok := keySeq(k)
		if ok && seq > cutoff {
			break
		}
		if v == nil { // never delete nested buckets here
			continue
		}
		victims = append(victims, append([]byte(nil), k...))
	}
	for _, k := range victims {
		if err := b.Delete(k); err != nil {
			return err
		}
	}
	return nil
}
