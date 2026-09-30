package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	deylog "github.com/localroot4/deyroute/internal/log"
)

// GetNode returns nodes/<id>.
func (s *Store) GetNode(id string) (NodeState, bool, error) {
	var n NodeState
	ok, err := s.getJSON("get node", BucketNodes, id, &n)
	return n, ok, err
}

// PutNode stores n under nodes/<n.ID>. Times are normalised to UTC and
// secrets in LastError are masked.
func (s *Store) PutNode(n NodeState) error {
	n.LastHeartbeat = utc(n.LastHeartbeat)
	n.OnlineSince = utc(n.OnlineSince)
	n.UDPCheckedAt = utc(n.UDPCheckedAt)
	n.LastError = clean(n.LastError)
	return s.putJSON("put node", BucketNodes, n.ID, n)
}

// ListNodes returns every node record ordered by id.
func (s *Store) ListNodes() ([]NodeState, error) {
	out := []NodeState{}
	err := s.db.View(func(tx *bolt.Tx) error {
		b, err := bucket(tx, BucketNodes)
		if err != nil {
			return err
		}
		return b.ForEach(func(k, v []byte) error {
			var n NodeState
			if err := json.Unmarshal(v, &n); err != nil {
				return fmt.Errorf("nodes/%s: %w", k, err)
			}
			out = append(out, n)
			return nil
		})
	})
	if err != nil {
		return nil, s.fail("list nodes", err)
	}
	return out, nil
}

// DeleteNode removes nodes/<id> together with the node's probe history and
// its backend control-port allocations ("<tunnel>/<id>/<transport>" keys of
// every tunnel), so a removed node frees its ports.
func (s *Store) DeleteNode(id string) error {
	if id == "" {
		return s.fail("delete node", errEmptyKey)
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		nodes, err := bucket(tx, BucketNodes)
		if err != nil {
			return err
		}
		if err := nodes.Delete([]byte(id)); err != nil {
			return err
		}
		match := func(k []byte) bool { return keyNode(string(k)) == id }
		if err := deleteSubBuckets(tx, BucketProbes, match); err != nil {
			return err
		}
		return deleteKeys(tx, BucketCtlPorts, match)
	})
	if err != nil {
		return s.fail("delete node", err)
	}
	return nil
}

// GetTunnel returns tunnels/<id>.
func (s *Store) GetTunnel(id string) (TunnelState, bool, error) {
	var t TunnelState
	ok, err := s.getJSON("get tunnel", BucketTunnels, id, &t)
	return t, ok, err
}

// PutTunnel stores t under tunnels/<t.ID>. Times (including quarantine and
// skip deadlines) are normalised to UTC; secrets in the free-text fields
// (LastProbeErr, TransitionCause, skip reasons) are masked.
func (s *Store) PutTunnel(t TunnelState) error {
	t.UpSince = utc(t.UpSince)
	t.StableSince = utc(t.StableSince)
	t.LastSwitch = utc(t.LastSwitch)
	t.DownRetryAt = utc(t.DownRetryAt)
	t.UpdatedAt = utc(t.UpdatedAt)
	t.LastProbeErr = clean(t.LastProbeErr)
	t.TransitionCause = clean(t.TransitionCause)
	// Copies, so the caller's slices and maps are never modified.
	if len(t.SwitchTimes) > 0 {
		st := make([]time.Time, len(t.SwitchTimes))
		for i, v := range t.SwitchTimes {
			st[i] = utc(v)
		}
		t.SwitchTimes = st
	}
	if len(t.Quarantine) > 0 {
		q := make(map[string]Quarantine, len(t.Quarantine))
		for k, v := range t.Quarantine {
			v.Until = utc(v.Until)
			q[k] = v
		}
		t.Quarantine = q
	}
	if len(t.Skipped) > 0 {
		sk := make(map[string]Skip, len(t.Skipped))
		for k, v := range t.Skipped {
			v.RecheckAt = utc(v.RecheckAt)
			v.Reason = clean(v.Reason)
			sk[k] = v
		}
		t.Skipped = sk
	}
	return s.putJSON("put tunnel", BucketTunnels, t.ID, t)
}

// ListTunnels returns every tunnel record ordered by id.
func (s *Store) ListTunnels() ([]TunnelState, error) {
	out := []TunnelState{}
	err := s.db.View(func(tx *bolt.Tx) error {
		b, err := bucket(tx, BucketTunnels)
		if err != nil {
			return err
		}
		return b.ForEach(func(k, v []byte) error {
			var t TunnelState
			if err := json.Unmarshal(v, &t); err != nil {
				return fmt.Errorf("tunnels/%s: %w", k, err)
			}
			out = append(out, t)
			return nil
		})
	})
	if err != nil {
		return nil, s.fail("list tunnels", err)
	}
	return out, nil
}

// DeleteTunnel removes everything that belongs to tunnel id in one
// transaction: tunnels/<id>, its probe histories (probes/<id>/…),
// metrics/<id>, its control ports ("<id>/" prefix) and its network index.
// The event history is kept.
func (s *Store) DeleteTunnel(id string) error {
	if id == "" {
		return s.fail("delete tunnel", errEmptyKey)
	}
	prefix := []byte(id + "/")
	match := func(k []byte) bool { return bytes.HasPrefix(k, prefix) }
	err := s.db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{BucketTunnels, BucketMetrics, BucketNetIdx} {
			b, err := bucket(tx, name)
			if err != nil {
				return err
			}
			if err := b.Delete([]byte(id)); err != nil {
				return err
			}
		}
		if err := deleteSubBuckets(tx, BucketProbes, match); err != nil {
			return err
		}
		return deleteKeys(tx, BucketCtlPorts, match)
	})
	if err != nil {
		return s.fail("delete tunnel", err)
	}
	return nil
}

// PutMetrics stores metrics/<tunnel>. A zero At is set to now (UTC).
func (s *Store) PutMetrics(tunnel string, m Metrics) error {
	if m.At.IsZero() {
		m.At = s.now()
	}
	m.At = m.At.UTC()
	return s.putJSON("put metrics", BucketMetrics, tunnel, m)
}

// GetMetrics returns metrics/<tunnel>.
func (s *Store) GetMetrics(tunnel string) (Metrics, bool, error) {
	var m Metrics
	ok, err := s.getJSON("get metrics", BucketMetrics, tunnel, &m)
	return m, ok, err
}

// ---------------------------------------------------------------- key helpers

// clean masks secrets in free text before it is stored. These fields are
// shown by the TUI, exported with the event history and copied into doctor
// bundles, where section 11 forbids tokens and keys (S26); backend and
// probe error texts can quote a URL, a flag or a config line.
func clean(s string) string { return deylog.Redact(s) }

// utc normalises t to UTC, keeping the zero time zero.
func utc(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC()
}

// Key builds the "<tunnel>/<node>/<transport>" key used by probes and
// control-port allocations. Transport ids contain a slash themselves
// ("backhaul/wssmux"), tunnel and node ids never do.
func Key(tunnel, node, transport string) string {
	return tunnel + "/" + node + "/" + transport
}

// SplitKey is the inverse of Key.
func SplitKey(key string) (tunnel, node, transport string, ok bool) {
	parts := strings.SplitN(key, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func keyNode(key string) string {
	_, n, _, ok := SplitKey(key)
	if !ok {
		return ""
	}
	return n
}

// deleteKeys deletes the plain keys of bucket name for which match is true.
func deleteKeys(tx *bolt.Tx, name string, match func(k []byte) bool) error {
	b, err := bucket(tx, name)
	if err != nil {
		return err
	}
	var victims [][]byte
	if err := b.ForEach(func(k, v []byte) error {
		if v != nil && match(k) {
			victims = append(victims, append([]byte(nil), k...))
		}
		return nil
	}); err != nil {
		return err
	}
	for _, k := range victims {
		if err := b.Delete(k); err != nil {
			return err
		}
	}
	return nil
}

// deleteSubBuckets deletes the nested buckets of bucket name whose name
// matches.
func deleteSubBuckets(tx *bolt.Tx, name string, match func(k []byte) bool) error {
	b, err := bucket(tx, name)
	if err != nil {
		return err
	}
	var victims [][]byte
	if err := b.ForEach(func(k, v []byte) error {
		if v == nil && match(k) {
			victims = append(victims, append([]byte(nil), k...))
		}
		return nil
	}); err != nil {
		return err
	}
	for _, k := range victims {
		if err := b.DeleteBucket(k); err != nil {
			return err
		}
	}
	return nil
}
