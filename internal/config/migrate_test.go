package config

import (
	"testing"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/stretchr/testify/require"
)

// withMigration installs a migration step for the duration of a test.
func withMigration(t *testing.T, from int, fn MigrationFunc) {
	t.Helper()
	migMu.RLock()
	prev, had := migrations[from]
	migMu.RUnlock()
	RegisterMigration(from, fn)
	t.Cleanup(func() {
		if had {
			RegisterMigration(from, prev)
		} else {
			RegisterMigration(from, nil)
		}
	})
}

// withSchema pretends this build's schema is v for the duration of a test.
func withSchema(t *testing.T, v int) {
	t.Helper()
	prev := currentSchema
	currentSchema = v
	t.Cleanup(func() { currentSchema = prev })
}

func TestMigrateFakeV0ToV1(t *testing.T) {
	// A fake pre-release v0 used "hub.title" instead of "hub.name".
	withMigration(t, 0, func(raw map[string]any) (map[string]any, error) {
		if hub, ok := raw["hub"].(map[string]any); ok {
			if title, ok := hub["title"]; ok {
				hub["name"] = title
				delete(hub, "title")
			}
		}
		return raw, nil
	})
	raw := map[string]any{"schema_version": 0, "hub": map[string]any{"title": "ir-1"}}
	out, err := Migrate(raw, 0)
	require.NoError(t, err)
	require.Equal(t, 1, out["schema_version"])
	require.Equal(t, map[string]any{"name": "ir-1"}, out["hub"])
}

func TestMigrateCurrentIsNoop(t *testing.T) {
	raw := map[string]any{"schema_version": 1, "role": "hub"}
	out, err := Migrate(raw, SchemaVersion)
	require.NoError(t, err)
	require.Equal(t, raw, out)
	out, err = Migrate(nil, SchemaVersion)
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestMigrateErrors(t *testing.T) {
	_, err := Migrate(map[string]any{}, SchemaVersion+1)
	requireCodes(t, err, deyerr.C019)

	// No step registered for 0 → 1.
	RegisterMigration(0, nil)
	_, err = Migrate(map[string]any{}, 0)
	requireCodes(t, err, deyerr.C006)
	e := firstErr(t, err)
	require.Equal(t, 0, e.Params["from"])
	require.Equal(t, 1, e.Params["to"])
	require.Contains(t, e.Message(), "0 -> 1")

	withMigration(t, 0, func(map[string]any) (map[string]any, error) { return nil, deyerr.Plain("boom") })
	_, err = Migrate(map[string]any{}, 0)
	requireCodes(t, err, deyerr.C006)
	require.Contains(t, err.Error(), "boom")

	withMigration(t, 0, func(map[string]any) (map[string]any, error) { return nil, nil })
	_, err = Migrate(map[string]any{}, 0)
	requireCodes(t, err, deyerr.C006)
}

func TestMigrateChain(t *testing.T) {
	withSchema(t, 3)
	var order []int
	for _, v := range []int{1, 2} {
		withMigration(t, v, func(raw map[string]any) (map[string]any, error) {
			order = append(order, v)
			require.Equal(t, v, raw["schema_version"], "schema_version is updated after each step")
			return raw, nil
		})
	}
	out, err := Migrate(map[string]any{"schema_version": 1}, 1)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, order)
	require.Equal(t, 3, out["schema_version"])
}

// TestParseRunsMigrations drives the Load path with a fake schema v2: an
// older v1 document is migrated, then decoded strictly and validated.
func TestParseRunsMigrations(t *testing.T) {
	withSchema(t, 2)
	withMigration(t, 1, func(raw map[string]any) (map[string]any, error) {
		hub := raw["hub"].(map[string]any)
		hub["name"] = hub["title"]
		delete(hub, "title")
		return raw, nil
	})
	doc := `schema_version: 1
role: hub
hub:
  title: ir-1
  public_ip: 5.6.7.8
`
	c, err := Parse([]byte(doc))
	require.NoError(t, err)
	require.Equal(t, 2, c.SchemaVersion)
	require.Equal(t, "ir-1", c.Hub.Name)

	v, err := SchemaVersionOf([]byte(doc))
	require.NoError(t, err)
	require.Equal(t, 1, v)

	// A migrated document that still has unknown keys is C001.
	_, err = Parse([]byte(doc + "  colour: blue\n"))
	requireCodes(t, err, deyerr.C001)
	require.Equal(t, "hub.colour", firstErr(t, err).Params["key"])
}

func TestParseMigrationFailure(t *testing.T) {
	withSchema(t, 2)
	withMigration(t, 1, func(map[string]any) (map[string]any, error) { return nil, deyerr.Plain("cannot convert") })
	_, err := Parse([]byte("schema_version: 1\nrole: hub\n"))
	requireCodes(t, err, deyerr.C006)
}
