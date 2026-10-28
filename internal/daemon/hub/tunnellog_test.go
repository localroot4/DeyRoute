package hub

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/systemd"
)

// An install made before tunnels/ became 01770 left it 0770: the hub adds
// the sticky bit when it starts (deyroute update restarts it), so the
// backends' user can no longer remove or replace root's tunnel logs.
func TestHubStartMakesTunnelLogDirSticky(t *testing.T) {
	logger, logs := newFrontLog()
	env, o := prepareEnv(t, nil, func(o *Options, _ string) { o.Logger = logger })
	dir := filepath.Join(env.root, systemd.TunnelLogDir)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.Chmod(dir, 0o770))
	env.startEnv(o)
	fi, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.ModeSticky|0o770, fi.Mode()&(os.ModePerm|os.ModeSticky))
	require.Contains(t, logs.String(), "tunnel log directory made sticky")
}

// A backend (user deyroute) planted a link to a root file at the log name of
// the next tunnel. Before systemd opens <tunnel>.log as root for the hub
// unit (it would follow the link), the hub replaces the link with its own
// regular file: the root file stays as it is, the tunnel comes UP and
// hub.log has the warning "tunnel log replaced" with the file and reason.
func TestHubStartReplacesPlantedTunnelLog(t *testing.T) {
	logger, logs := newFrontLog()
	te := startTunnelHub(t, func(o *Options, _ string) { o.Logger = logger })
	te.tunnelNode("de-1")
	dir := filepath.Join(te.root, systemd.TunnelLogDir)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.Chmod(dir, os.ModeSticky|0o770))
	victim := filepath.Join(te.root, "etc", "cron.d", "x")
	require.NoError(t, os.MkdirAll(filepath.Dir(victim), 0o755))
	victimData := []byte("* * * * * root /bin/true\n")
	require.NoError(t, os.WriteFile(victim, victimData, 0o644))
	logFile := filepath.Join(te.root, systemd.TunnelLogFile("t1"))
	require.NoError(t, os.Symlink(victim, logFile))

	te.addTunnelUp(api.TunnelAddRequest{
		ID: "t1", Name: "t1", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		Rungs: []string{trAlpha}, Failover: fastFailover(false),
	})
	require.Equal(t, "hub start t1.de-1.tfa-alpha", te.order()[0], "the hub side started")
	fi, err := os.Lstat(logFile)
	require.NoError(t, err)
	require.True(t, fi.Mode().IsRegular(), "t1.log is %v", fi.Mode())
	st, ok := fi.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	require.Equal(t, uint64(1), uint64(st.Nlink))
	require.Equal(t, os.Geteuid(), int(st.Uid))
	data, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, string(victimData), string(data), "the root file is unchanged")
	want := `msg="tunnel log replaced" file=` + logFile + " reason=symlink"
	require.Equal(t, 1, strings.Count(logs.String(), want), "one warning: %s", logs.String())

	// A restart of the hub unit (a port added) prepares the log the same way.
	require.NoError(t, os.Remove(logFile))
	require.NoError(t, syscall.Mkfifo(logFile, 0o666))
	var steps stepLog
	_, err = te.client.PortAdd(ctxT(t), "t1", []api.PortSpec{{Listen: freePort(t)}}, steps.add)
	require.NoError(t, err, "%v", steps.finished())
	require.Equal(t, 1, te.sd.count("restart", hubUnit("t1", "de-1", trAlpha)))
	fi, err = os.Lstat(logFile)
	require.NoError(t, err)
	require.True(t, fi.Mode().IsRegular(), "t1.log is %v", fi.Mode())
	require.Contains(t, logs.String(), `msg="tunnel log replaced" file=`+logFile+" reason=not_regular")
}
