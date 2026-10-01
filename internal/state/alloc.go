package state

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strconv"

	bolt "go.etcd.io/bbolt"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Backend control-port range (section 10/11 firewall table, QUESTIONS.md B).
const (
	CtlPortLow  = 30000
	CtlPortHigh = 31999
)

var errEmptyPrefix = stderrors.New("empty prefix would release every allocation")

// AllocCtlPort returns the backend control port allocated to key (normally
// Key(tunnel, node, transport)). An existing allocation inside [lo, hi] is
// returned unchanged, even when busy reports it in use (the owner of the
// allocation is usually the one holding it). Otherwise the lowest port in
// [lo, hi] that is not allocated to another key and for which busy (may be
// nil) reports false is allocated and persisted. DEY-P020 is returned when
// no port is left, DEY-P017 for an invalid range.
//
// busy runs inside the allocating write transaction, so concurrent
// allocations never hand out the same port; it must be quick and must not
// call back into the Store (a nested write would deadlock).
func (s *Store) AllocCtlPort(key string, lo, hi int, busy func(port int) bool) (int, error) {
	if key == "" {
		return 0, s.fail("alloc ctl port", errEmptyKey)
	}
	if lo < 1 || hi > 65535 || lo > hi {
		return 0, deyerr.New(deyerr.P017, deyerr.Params{"input": fmt.Sprintf("%d-%d", lo, hi)})
	}
	var port int
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := bucket(tx, BucketCtlPorts)
		if err != nil {
			return err
		}
		used := make(map[int]bool)
		existing := 0
		if err := b.ForEach(func(k, v []byte) error {
			var p int
			if err := json.Unmarshal(v, &p); err != nil {
				return fmt.Errorf("ctlports/%s: %w", k, err)
			}
			if string(k) == key {
				existing = p
				return nil
			}
			used[p] = true
			return nil
		}); err != nil {
			return err
		}
		if existing >= lo && existing <= hi && !used[existing] {
			port = existing
			return nil
		}
		for p := lo; p <= hi; p++ {
			if used[p] || (busy != nil && busy(p)) {
				continue
			}
			port = p
			return b.Put([]byte(key), []byte(strconv.Itoa(p)))
		}
		return deyerr.New(deyerr.P020, deyerr.Params{"key": key})
	})
	if err != nil {
		return 0, s.fail("alloc ctl port", err)
	}
	return port, nil
}

// CtlPorts returns every control-port allocation (key → port).
func (s *Store) CtlPorts() (map[string]int, error) {
	out := map[string]int{}
	err := s.db.View(func(tx *bolt.Tx) error {
		b, err := bucket(tx, BucketCtlPorts)
		if err != nil {
			return err
		}
		return b.ForEach(func(k, v []byte) error {
			var p int
			if err := json.Unmarshal(v, &p); err != nil {
				return fmt.Errorf("ctlports/%s: %w", k, err)
			}
			out[string(k)] = p
			return nil
		})
	})
	if err != nil {
		return nil, s.fail("list ctl ports", err)
	}
	return out, nil
}

// ReleaseCtlPorts deletes every allocation whose key starts with prefix,
// e.g. "main/" when tunnel main is deleted or "main/de-1/" when node de-1
// leaves tunnel main. An empty prefix is rejected.
func (s *Store) ReleaseCtlPorts(prefix string) error {
	if prefix == "" {
		return s.fail("release ctl ports", errEmptyPrefix)
	}
	p := []byte(prefix)
	err := s.db.Update(func(tx *bolt.Tx) error {
		return deleteKeys(tx, BucketCtlPorts, func(k []byte) bool { return bytes.HasPrefix(k, p) })
	})
	if err != nil {
		return s.fail("release ctl ports", err)
	}
	return nil
}

// ReleaseCtlPort deletes the allocation of exactly key (a transport removed
// from a ladder). No error when absent.
func (s *Store) ReleaseCtlPort(key string) error {
	return s.deleteKey("release ctl port", BucketCtlPorts, key)
}

// AllocNetIndex returns a stable small integer in 1..max for tunnel (used to
// derive per-tunnel WireGuard subnets). An existing index inside the range
// is returned unchanged; otherwise the lowest free one is persisted.
// DEY-P030 is returned when all max indexes are taken.
func (s *Store) AllocNetIndex(tunnel string, maxIndex int) (int, error) {
	if tunnel == "" {
		return 0, s.fail("alloc net index", errEmptyKey)
	}
	if maxIndex < 1 {
		return 0, s.fail("alloc net index", fmt.Errorf("invalid max %d", maxIndex))
	}
	var idx int
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := bucket(tx, BucketNetIdx)
		if err != nil {
			return err
		}
		used := make(map[int]bool)
		existing := 0
		if err := b.ForEach(func(k, v []byte) error {
			var n int
			if err := json.Unmarshal(v, &n); err != nil {
				return fmt.Errorf("netidx/%s: %w", k, err)
			}
			if string(k) == tunnel {
				existing = n
				return nil
			}
			used[n] = true
			return nil
		}); err != nil {
			return err
		}
		if existing >= 1 && existing <= maxIndex && !used[existing] {
			idx = existing
			return nil
		}
		for n := 1; n <= maxIndex; n++ {
			if !used[n] {
				idx = n
				return b.Put([]byte(tunnel), []byte(strconv.Itoa(n)))
			}
		}
		return deyerr.New(deyerr.P030, deyerr.Params{"tunnel": tunnel, "max": maxIndex})
	})
	if err != nil {
		return 0, s.fail("alloc net index", err)
	}
	return idx, nil
}

// ReleaseNetIndex frees the index of tunnel (no error when absent).
func (s *Store) ReleaseNetIndex(tunnel string) error {
	return s.deleteKey("release net index", BucketNetIdx, tunnel)
}

// NetIndexes returns every network-index allocation (tunnel → index).
func (s *Store) NetIndexes() (map[string]int, error) {
	out := map[string]int{}
	err := s.db.View(func(tx *bolt.Tx) error {
		b, err := bucket(tx, BucketNetIdx)
		if err != nil {
			return err
		}
		return b.ForEach(func(k, v []byte) error {
			var n int
			if err := json.Unmarshal(v, &n); err != nil {
				return fmt.Errorf("netidx/%s: %w", k, err)
			}
			out[string(k)] = n
			return nil
		})
	})
	if err != nil {
		return nil, s.fail("list net indexes", err)
	}
	return out, nil
}
