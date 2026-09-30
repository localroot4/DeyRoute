package config

import (
	"sync"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// MigrationFunc converts a raw config document from schema version N to N+1.
// It may modify raw in place and returns the converted document. It must not
// set schema_version; Migrate does that after each step.
type MigrationFunc func(raw map[string]any) (map[string]any, error)

var (
	migMu sync.RWMutex
	// migrations is keyed by the version a step migrates FROM. Version 1 is
	// the first published schema, so the registry is empty today; a future
	// schema 2 adds migrations[1].
	migrations = map[int]MigrationFunc{}
)

// RegisterMigration installs the step that converts version from to from+1.
// It is meant to be called from init() of this package when a new schema
// version is introduced; registering the same step twice replaces it.
func RegisterMigration(from int, fn MigrationFunc) {
	migMu.Lock()
	defer migMu.Unlock()
	if fn == nil {
		delete(migrations, from)
		return
	}
	migrations[from] = fn
}

func migrationFor(from int) (MigrationFunc, bool) {
	migMu.RLock()
	defer migMu.RUnlock()
	fn, ok := migrations[from]
	return fn, ok
}

// Migrate converts raw (a decoded config document) from schema version from
// to the current SchemaVersion by running every registered step in order and
// updating schema_version after each one. raw may be modified.
//
// Errors: DEY-C019 when from is newer than this build, DEY-C006 when a step
// is missing or fails.
func Migrate(raw map[string]any, from int) (map[string]any, error) {
	to := currentSchema
	if from > to {
		return nil, deyerr.New(deyerr.C019, deyerr.Params{"version": from})
	}
	if raw == nil {
		raw = map[string]any{}
	}
	for v := from; v < to; v++ {
		fn, ok := migrationFor(v)
		if !ok {
			return nil, wrapDetail(deyerr.C006, deyerr.Plain("no migration is registered for this step"),
				deyerr.Params{"from": v, "to": v + 1})
		}
		out, err := fn(raw)
		if err != nil {
			return nil, wrapDetail(deyerr.C006, err, deyerr.Params{"from": v, "to": v + 1})
		}
		if out == nil {
			return nil, wrapDetail(deyerr.C006, deyerr.Plain("the migration returned no document"),
				deyerr.Params{"from": v, "to": v + 1})
		}
		out["schema_version"] = v + 1
		raw = out
	}
	return raw, nil
}
