package exec

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSplitPair(t *testing.T) {
	got, err := SplitPair([]string{"/opt/b", "-c", "a.toml", "--", "/opt/b", "-c", "b.toml"})
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
		_, err := SplitPair(args)
		require.Error(t, err, "%q", args)
	}
}

func TestRunPairStopsTheOthersWhenOneExits(t *testing.T) {
	var out bytes.Buffer
	start := time.Now()
	err := RunPair(context.Background(), [][]string{
		{"/bin/sh", "-c", "exec sleep 30"},
		{"/bin/sh", "-c", "echo up; exit 3"},
	}, &out, &out)
	require.ErrorContains(t, err, "/bin/sh exited")
	require.Less(t, time.Since(start), 10*time.Second, "the sleeping process is stopped")
	require.Contains(t, out.String(), "up")

	// A clean exit fails the set too.
	err = RunPair(context.Background(), [][]string{{"/bin/sh", "-c", "exec sleep 30"}, {"/bin/sh", "-c", "exit 0"}}, &out, &out)
	require.ErrorContains(t, err, "exited")
}

func TestRunPairReturnsNilWhenStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	var out bytes.Buffer
	err := RunPair(ctx, [][]string{{"/bin/sh", "-c", "exec sleep 30"}, {"/bin/sh", "-c", "exec sleep 30"}}, &out, &out)
	require.NoError(t, err)
}

func TestRunPairStartFailureStopsTheStarted(t *testing.T) {
	var out bytes.Buffer
	err := RunPair(context.Background(), [][]string{{"/bin/sh", "-c", "exec sleep 30"}, {"/nonexistent/deyroute-test"}}, &out, &out)
	require.ErrorContains(t, err, "start /nonexistent/deyroute-test")
}
