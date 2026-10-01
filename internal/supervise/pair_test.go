package supervise

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSplit(t *testing.T) {
	got, err := Split([]string{"/opt/b", "-c", "a.toml", "--", "/opt/b", "-c", "b.toml"})
	require.NoError(t, err)
	require.Equal(t, [][]string{{"/opt/b", "-c", "a.toml"}, {"/opt/b", "-c", "b.toml"}}, got)

	for _, args := range [][]string{
		nil,
		{"/opt/b", "-c", "a.toml"},
		{"/opt/b", "--"},
		{"--", "/opt/b"},
		{"b", "--", "/opt/b"},
		{"/opt/../b", "--", "/opt/b"},
	} {
		_, err := Split(args)
		require.Error(t, err, "%q", args)
	}
}

func TestRunStopsTheOthersWhenOneExits(t *testing.T) {
	var out bytes.Buffer
	start := time.Now()
	err := Run(context.Background(), [][]string{
		{"/bin/sh", "-c", "exec sleep 30"},
		{"/bin/sh", "-c", "echo up; exit 3"},
	}, &out, &out)
	require.ErrorContains(t, err, "/bin/sh exited")
	require.Less(t, time.Since(start), 10*time.Second, "the sleeping process is stopped")
	require.Contains(t, out.String(), "up")

	// A clean exit fails the set too.
	err = Run(context.Background(), [][]string{{"/bin/sh", "-c", "exec sleep 30"}, {"/bin/sh", "-c", "exit 0"}}, &out, &out)
	require.ErrorContains(t, err, "exited")
}

func TestRunReturnsNilWhenStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	var out bytes.Buffer
	err := Run(ctx, [][]string{{"/bin/sh", "-c", "exec sleep 30"}, {"/bin/sh", "-c", "exec sleep 30"}}, &out, &out)
	require.NoError(t, err)
}

func TestRunStartFailureStopsTheStarted(t *testing.T) {
	var out bytes.Buffer
	err := Run(context.Background(), [][]string{{"/bin/sh", "-c", "exec sleep 30"}, {"/nonexistent/deyroute-test"}}, &out, &out)
	require.ErrorContains(t, err, "start /nonexistent/deyroute-test")
}
