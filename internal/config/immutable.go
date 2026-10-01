package config

import (
	"errors"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// CheckImmutable enforces "ids never change after creation" (section 4) for
// an edit from prev to next (e.g. `deyroute config edit`). It reports DEY-C018
// when the node's own id changed (node role), and when a hub nodes: entry
// keeps its certificate fingerprint but has a different id. A changed tunnel
// id is indistinguishable from delete + add and is therefore allowed here;
// the daemon treats it as such. nil configs are ignored.
func CheckImmutable(prev, next *Config) error {
	if prev == nil || next == nil {
		return nil
	}
	var errs []error
	if prev.Node != nil && next.Node != nil && prev.Node.ID != "" && prev.Node.ID != next.Node.ID {
		errs = append(errs, deyerr.New(deyerr.C018, deyerr.Params{"kind": "node", "id": prev.Node.ID}))
	}
	byFP := map[string]string{}
	for _, n := range prev.Nodes {
		if ValidFingerprint(n.CertFingerprint) {
			byFP[n.CertFingerprint] = n.ID
		}
	}
	for _, n := range next.Nodes {
		if old, ok := byFP[n.CertFingerprint]; ok && old != n.ID {
			if _, still := next.NodeByID(old); !still {
				errs = append(errs, deyerr.New(deyerr.C018, deyerr.Params{"kind": "node", "id": old}))
			}
		}
	}
	return errors.Join(errs...)
}
