package systemd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

const (
	mib = uint64(1) << 20
	gib = uint64(1) << 30
)

// serializeAuto writes the rendered drop-ins in path order.
func serializeAuto(files map[string][]byte) []byte {
	var b bytes.Buffer
	for _, p := range AutoDropInPaths() {
		if data, ok := files[p]; ok {
			fmt.Fprintf(&b, "=== %s\n%s", p, data)
		}
	}
	return b.Bytes()
}

func TestRenderAutoDropInsGolden(t *testing.T) {
	cases := []struct {
		name string
		r    Resources
	}{
		{"hub-512m", Resources{MemBytes: 512 * mib, Role: config.RoleHub, NrOpen: 1 << 30}},
		{"hub-2g", Resources{MemBytes: 2 * gib, Role: config.RoleHub}},
		{"hub-8g", Resources{MemBytes: 8 * gib, Role: config.RoleHub, NrOpen: TemplateNOFILE}},
		{"node-1g", Resources{MemBytes: gib, Role: config.RoleNode}},
		{"node-container-nofile", Resources{MemBytes: 4 * gib, Role: config.RoleNode, NrOpen: 524288}},
		{"hub-unknown-ram", Resources{Role: config.RoleHub}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := RenderAutoDropIns(c.r)
			require.Equal(t, files, RenderAutoDropIns(c.r), "deterministic")
			for p, data := range files {
				require.Contains(t, AutoDropInPaths(), p)
				require.True(t, strings.HasPrefix(string(data), AutoHeader+"\n"), "%s starts with the header", p)
				require.NotContains(t, string(data), "TasksMax", "never lowers systemd's TasksMax default")
				require.NotContains(t, string(data), "MemoryMax", "no hard memory cap (crash loops)")
				require.NotContains(t, string(data), "OOMScoreAdjust=-", "never a negative OOM score")
			}
			got := serializeAuto(files)
			path := filepath.Join("testdata", "auto-"+c.name+".golden")
			if *update {
				require.NoError(t, os.WriteFile(path, got, 0o600))
				return
			}
			want, err := os.ReadFile(path) // #nosec G304 -- test fixture path
			require.NoError(t, err, "run: go test ./internal/systemd -update")
			require.Equal(t, string(want), string(got))
		})
	}
}

func TestAutoSettingsPolicy(t *testing.T) {
	// The OOM decision: tunnels +300, hub and node never get an OOM score.
	for _, role := range []string{config.RoleHub, config.RoleNode} {
		for _, s := range AutoSettings(Resources{MemBytes: 2 * gib, Role: role, NrOpen: 65536}) {
			if s.Key == "OOMScoreAdjust" {
				require.Equal(t, TunTemplate, s.Unit)
				require.Equal(t, "300", s.Value)
			}
			require.Contains(t, []string{EffectNow, EffectNextStart}, s.Effect)
			if s.Unit == TunnelSlice {
				require.Equal(t, EffectNow, s.Effect, "systemd applies slice limits at daemon-reload")
			}
		}
	}
	// The node gets no drop-in at all unless LimitNOFILE must be clamped.
	files := RenderAutoDropIns(Resources{MemBytes: 2 * gib, Role: config.RoleNode})
	require.NotContains(t, files, AutoDropInPath(NodeUnit))
	require.NotContains(t, files, AutoDropInPath(HubUnit))

	require.Equal(t, "128MiB", HubGOMEMLIMIT(gib))
	require.Equal(t, "256MiB", HubGOMEMLIMIT(gib+1))
	require.Equal(t, "256MiB", HubGOMEMLIMIT(4*gib))
	require.Equal(t, "512MiB", HubGOMEMLIMIT(4*gib+1))
	require.Equal(t, "768M", TunnelMemoryHigh(gib))
	require.Equal(t, "", TunnelMemoryHigh(0))
	require.Equal(t, "", TunnelMemoryHigh(mib))
	require.Equal(t, "", nofileClamp(0))
	require.Equal(t, "", nofileClamp(TemplateNOFILE))
	require.Equal(t, "65536", nofileClamp(65536))
	require.Equal(t, "GOMEMLIMIT=128MiB", AutoSetting{Key: "Environment", Value: "GOMEMLIMIT=128MiB"}.Directive()[len("Environment="):])
}

func TestWriteRemoveAutoDropIns(t *testing.T) {
	m, f := newManager(t)
	f.On("systemctl daemon-reload")
	ctx := context.Background()
	unitDir := filepath.Join(m.Root, UnitDir)

	// An instance drop-in and a foreign drop-in in the hub's directory are
	// never touched.
	_, err := m.WriteDropIn("main.de-1.backhaul-tcp", []byte("[Service]\n"))
	require.NoError(t, err)
	foreign := filepath.Join(unitDir, HubUnit+".d", "override.conf")
	require.NoError(t, os.MkdirAll(filepath.Dir(foreign), 0o750))
	require.NoError(t, os.WriteFile(foreign, []byte("[Service]\n"), 0o600))

	big := RenderAutoDropIns(Resources{MemBytes: 8 * gib, Role: config.RoleHub, NrOpen: 65536})
	require.Empty(t, filterMissing(AutoDropInPaths(), big, config.RoleHub), "every hub path is rendered")
	require.Len(t, m.PendingAutoDropIns(big), len(big))

	changed, err := m.WriteAutoDropIns(ctx, big)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 1, f.Count("systemctl daemon-reload"))
	for p, data := range big {
		got, err := os.ReadFile(filepath.Join(m.Root, p)) // #nosec G304 -- test path
		require.NoError(t, err)
		require.Equal(t, data, got)
		fi, err := os.Stat(filepath.Join(m.Root, p))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o644), fi.Mode().Perm())
	}
	require.Empty(t, m.PendingAutoDropIns(big))

	// Idempotent: no write, no reload.
	changed, err = m.WriteAutoDropIns(ctx, big)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, 1, f.Count("systemctl daemon-reload"))

	// Another role and fewer files: the node drop-in (LimitNOFILE clamp)
	// is written, then removed when nr_open no longer needs it, and the
	// hub drop-in goes; each step is a single reload.
	clamp := RenderAutoDropIns(Resources{MemBytes: 8 * gib, Role: config.RoleNode, NrOpen: 65536})
	changed, err = m.WriteAutoDropIns(ctx, clamp)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 2, f.Count("systemctl daemon-reload"))
	require.FileExists(t, filepath.Join(m.Root, AutoDropInPath(NodeUnit)))
	require.NoFileExists(t, filepath.Join(m.Root, AutoDropInPath(HubUnit)))
	require.FileExists(t, foreign, "a foreign drop-in keeps its directory")
	plain := RenderAutoDropIns(Resources{MemBytes: 8 * gib, Role: config.RoleNode})
	changed, err = m.WriteAutoDropIns(ctx, plain)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 3, f.Count("systemctl daemon-reload"))
	require.NoFileExists(t, filepath.Join(m.Root, AutoDropInPath(NodeUnit)))
	require.NoDirExists(t, filepath.Join(unitDir, NodeUnit+".d"), "an emptied drop-in directory is removed")

	// A path outside the known set is refused before anything is written.
	_, err = m.WriteAutoDropIns(ctx, map[string][]byte{"/etc/systemd/system/ssh.service.d/x.conf": []byte("x")})
	require.True(t, deyerr.HasCode(err, deyerr.X034), "%v", err)
	require.NoDirExists(t, filepath.Join(unitDir, "ssh.service.d"))

	// Remove: everything goes with one reload; directories left empty are
	// removed, the foreign drop-in and the instance drop-in stay.
	changed, err = m.RemoveAutoDropIns(ctx)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 4, f.Count("systemctl daemon-reload"))
	for _, p := range AutoDropInPaths() {
		require.NoFileExists(t, filepath.Join(m.Root, p))
	}
	require.NoDirExists(t, filepath.Join(unitDir, TunTemplate+".d"))
	require.FileExists(t, foreign)
	require.FileExists(t, m.DropInPath("main.de-1.backhaul-tcp"))

	// Removing again changes nothing.
	changed, err = m.RemoveAutoDropIns(ctx)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, 4, f.Count("systemctl daemon-reload"))

	// Only daemon-reload was ever run: no unit was started, stopped or
	// restarted.
	for _, l := range f.Lines() {
		require.Equal(t, "systemctl daemon-reload", l)
	}
}

func TestWriteAutoDropInsReloadFailure(t *testing.T) {
	m, f := newManager(t)
	f.On("systemctl daemon-reload", exec.Fail(1, "Failed to reload daemon"))
	changed, err := m.WriteAutoDropIns(context.Background(), RenderAutoDropIns(Resources{MemBytes: gib, Role: config.RoleNode}))
	require.Error(t, err)
	require.True(t, changed, "the files were written; the caller retries the reload")
}

// filterMissing returns the paths a hub rendering is expected to contain
// that files lacks (the node drop-in belongs to the other role).
func filterMissing(paths []string, files map[string][]byte, role string) []string {
	var out []string
	for _, p := range paths {
		if role == config.RoleHub && p == AutoDropInPath(NodeUnit) {
			continue
		}
		if _, ok := files[p]; !ok {
			out = append(out, p)
		}
	}
	return out
}
