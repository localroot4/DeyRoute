package node

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/systemd"
)

// An install made before tunnels/ became 01770 left it 0770: the agent adds
// the sticky bit when it starts (deyroute update restarts it), so the
// backends' user can no longer remove or replace root's tunnel logs.
func TestNodeStartMakesTunnelLogDirSticky(t *testing.T) {
	e := newEnv(t)
	dir := e.path(systemd.TunnelLogDir)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.Chmod(dir, 0o770))
	e.start()
	fi, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.ModeSticky|0o770, fi.Mode()&(os.ModePerm|os.ModeSticky))
	require.Contains(t, e.logs.String(), "tunnel log directory made sticky")
}

// replacedWarnings returns "<file> <reason>" of every "tunnel log replaced"
// warning in the agent's log.
func replacedWarnings(t *testing.T, e *env) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(e.logs.String()), "\n") {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m), "line %q", line)
		if m["msg"] == "tunnel log replaced" {
			require.Equal(t, "warn", m["level"])
			out = append(out, fmt.Sprint(m["file"], " ", m["reason"]))
		}
	}
	return out
}

// requireOwnLog checks that p is a regular file of this process's user with
// one link: what systemd may open as root for StandardOutput=append:.
func requireOwnLog(t *testing.T, p string) {
	t.Helper()
	fi, err := os.Lstat(p)
	require.NoError(t, err)
	require.True(t, fi.Mode().IsRegular(), "%s is %v", p, fi.Mode())
	st, ok := fi.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	require.Equal(t, uint64(1), uint64(st.Nlink))
	require.Equal(t, os.Geteuid(), int(st.Uid))
}

// A backend (user deyroute) planted a link to a root file at the log name of
// a tunnel the hub starts next on this node. Before systemd opens
// <tunnel>.log as root (it would follow the link and create the target),
// the agent replaces it with its own regular file, on unit.start and on
// unit.restart: the root file stays as it is and node.log has the warning
// "tunnel log replaced" once per file and reason.
func TestNodeUnitStartReplacesPlantedTunnelLog(t *testing.T) {
	e := newEnv(t)
	s := e.start()
	args := renderArgs("main", "backhaul", "wssmux")
	_, err := call[json.RawMessage](t, s, api.CmdBackendRender, args)
	require.NoError(t, err)
	victimData := "* * * * * root /bin/true\n"
	victim := e.path("/etc/cron.d/x")
	writeFile(t, victim, victimData, 0o644)
	logFile := e.path(systemd.TunnelLogFile("main"))
	require.NoError(t, os.Symlink(victim, logFile))

	_, err = call[api.UnitStatus](t, s, api.CmdUnitStart, api.UnitArgs{Instance: args.Instance})
	require.NoError(t, err)
	require.Equal(t, "active", e.sys.state(unitOf(args)))
	requireOwnLog(t, logFile)
	data, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, victimData, string(data), "the root file is unchanged")
	require.Equal(t, []string{logFile + " symlink"}, replacedWarnings(t, e))

	// unit.restart: a FIFO this time; then a link again (warned once).
	require.NoError(t, os.Remove(logFile))
	require.NoError(t, syscall.Mkfifo(logFile, 0o666))
	_, err = call[api.UnitStatus](t, s, api.CmdUnitRestart, api.UnitArgs{Instance: args.Instance})
	require.NoError(t, err)
	requireOwnLog(t, logFile)
	require.NoError(t, os.Remove(logFile))
	require.NoError(t, os.Symlink(victim, logFile))
	_, err = call[api.UnitStatus](t, s, api.CmdUnitRestart, api.UnitArgs{Instance: args.Instance})
	require.NoError(t, err)
	requireOwnLog(t, logFile)
	require.Equal(t, []string{logFile + " symlink", logFile + " not_regular"}, replacedWarnings(t, e))

	// The agent's own log is kept on the next start.
	require.NoError(t, os.WriteFile(logFile, []byte("backend output\n"), 0o644))
	_, err = call[api.UnitStatus](t, s, api.CmdUnitRestart, api.UnitArgs{Instance: args.Instance})
	require.NoError(t, err)
	data, err = os.ReadFile(logFile)
	require.NoError(t, err)
	require.Equal(t, "backend output\n", string(data))
}

// A backend planted a link at the log name of a tunnel that runs on this
// node while tunnels/ was still 0770 (an install before the update, where
// the backends' user can also remove root's log). systemd's own restarts of
// a unit (Restart=always) open that name as root without a unit.start, so
// when the agent starts, right after it made the directory sticky, it
// prepares the log of every tunnel it has instances of: the link is
// replaced by the agent's own regular file, the root file stays as it is
// and node.log has "tunnel log replaced". The log name of a tunnel the node
// does not run is left for its unit.start.
func TestNodeStartReplacesPlantedLogOfKnownTunnel(t *testing.T) {
	e := newEnv(t)
	s, stop := e.startStoppable()
	args := renderArgs("main", "backhaul", "wssmux")
	_, err := call[json.RawMessage](t, s, api.CmdBackendRender, args)
	require.NoError(t, err)
	_, err = call[api.UnitStatus](t, s, api.CmdUnitStart, api.UnitArgs{Instance: args.Instance})
	require.NoError(t, err)
	stop()

	dir := e.path(systemd.TunnelLogDir)
	require.NoError(t, os.Chmod(dir, 0o770))
	victimData := "* * * * * root /bin/true\n"
	victim := e.path("/etc/cron.d/x")
	writeFile(t, victim, victimData, 0o644)
	logFile := e.path(systemd.TunnelLogFile("main"))
	require.NoError(t, os.Remove(logFile))
	require.NoError(t, os.Symlink(victim, logFile))
	other := e.path(systemd.TunnelLogFile("other"))
	require.NoError(t, os.Symlink(victim, other))

	e.start()
	requireOwnLog(t, logFile)
	data, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, victimData, string(data), "the root file is unchanged")
	require.Equal(t, []string{logFile + " symlink"}, replacedWarnings(t, e))
	fi, err := os.Lstat(other)
	require.NoError(t, err)
	require.NotZero(t, fi.Mode()&os.ModeSymlink, "the log name of another tunnel is left as it is")
	fi, err = os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.ModeSticky|0o770, fi.Mode()&(os.ModePerm|os.ModeSticky))
}
