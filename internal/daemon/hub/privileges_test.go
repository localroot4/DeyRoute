package hub

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/systemd"
)

// The hub applies the nodes' privilege allowlist to its own side before it
// writes anything: a renderer that gives a backend more than its row fails
// at render time with DEY-X034 and leaves no file and no drop-in behind.
func TestHubSideOutsideTheAllowlistIsNotWritten(t *testing.T) {
	root := t.TempDir()
	sd := &systemd.Manager{Root: root}
	w := &render.HubWriter{Root: root, Systemd: sd,
		Chown:       func(string, int, int) error { return nil },
		DeyrouteIDs: func() (int, int, error) { return 0, 0, nil }}
	dir := render.ConfigDir("xray", "main", "de-1", "reality")
	inst := systemd.InstanceName("main", "de-1", "xray/reality")
	cand := func(u backend.UnitSpec) *render.Candidate {
		return &render.Candidate{
			Node: "de-1", TransportID: "xray/reality", Backend: "xray",
			Transport: backend.Transport{Backend: "xray", Name: "reality"},
			Hub: render.Side{Instance: inst, ConfigDir: dir, Unit: u,
				Files:  map[string][]byte{"config.json": []byte("{}\n")},
				DropIn: systemd.RenderDropIn(u, dir, "/var/log/deyroute/tunnels/main.log")},
		}
	}
	plain := backend.UnitSpec{ExecStart: []string{config.BinDir + "/xray/v25.10.15/xray", "run", "-c", dir + "/config.json"}}

	bad := map[string]func(u *backend.UnitSpec){
		"RunAsRoot":       func(u *backend.UnitSpec) { u.RunAsRoot = true },
		"ExtraCaps":       func(u *backend.UnitSpec) { u.ExtraCaps = []string{"CAP_SYS_ADMIN"} },
		"AddressFamilies": func(u *backend.UnitSpec) { u.AddressFamilies = []string{"AF_NETLINK"} },
		"DropHardening":   func(u *backend.UnitSpec) { u.DropHardening = map[string]string{"NoNewPrivileges": "x"} },
		"ReadWritePaths":  func(u *backend.UnitSpec) { u.ReadWritePaths = []string{"/etc"} },
	}
	for field, f := range bad {
		u := plain
		f(&u)
		_, err := writeHubSide(context.Background(), w, cand(u))
		require.Truef(t, deyerr.HasCode(err, deyerr.X034), "%s: want DEY-X034, got %v", field, err)
		e := deyerr.As(err)
		require.Contains(t, e.Message(), field)
		require.Contains(t, e.Why(), "backend xray/reality may not")
		_, statErr := os.Stat(filepath.Join(root, dir))
		require.True(t, os.IsNotExist(statErr), "%s: the config directory is not created", field)
		_, statErr = os.Stat(sd.DropInPath(inst))
		require.True(t, os.IsNotExist(statErr), "%s: no drop-in is written", field)
	}

	changed, err := writeHubSide(context.Background(), w, cand(plain))
	require.NoError(t, err)
	require.True(t, changed)
	_, err = os.Stat(sd.DropInPath(inst))
	require.NoError(t, err)
}
