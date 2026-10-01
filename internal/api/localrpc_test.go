package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

func requireCode(t *testing.T, err error, code deyerr.Code) *deyerr.Error {
	t.Helper()
	require.Error(t, err)
	require.Truef(t, deyerr.HasCode(err, code), "want %s, got %v", code, err)
	return deyerr.As(err)
}

// serve starts impl on a socket and returns the socket path.
func serve(t *testing.T, impl api.Local) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "d.sock")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- api.ServeLocalReady(ctx, sock, api.NewLocalHandler(impl, dlog.Discard()), dlog.Discard(), func() { close(ready) })
	}()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
		_, err := os.Lstat(sock)
		require.True(t, os.IsNotExist(err), "socket must be removed")
	})
	return sock
}

func TestLocalRoundTripNonStreaming(t *testing.T) {
	var gotRename []string
	stub := &apitest.Stub{
		StatusFn: func(context.Context) (api.Status, error) {
			return api.Status{Schema: api.JSONSchemaVersion, Role: "hub", Version: "1.2.3",
				Tunnels: []api.TunnelInfo{{ID: "main", Ports: []api.PortMapDTO{{Listen: 443, Proto: "tcp"}}}}}, nil
		},
		NodeRenameFn: func(_ context.Context, id, name string) error {
			gotRename = []string{id, name}
			return nil
		},
		NodeRemoveFn: func(_ context.Context, id string) error {
			return deyerr.New(deyerr.N008, deyerr.Params{"node": id})
		},
		NodeJoinCommandFn: func(_ context.Context, ttl time.Duration) (api.JoinCommand, error) {
			return api.JoinCommand{Link: ttl.String()}, nil
		},
		EventsFn: func(_ context.Context, q api.EventQuery) ([]state.Event, error) {
			return []state.Event{{Seq: 7, Tunnel: q.Tunnel}}, nil
		},
		NodeListFn: func(context.Context) ([]api.NodeInfo, error) { return nil, nil },
		PortSuggestFn: func(context.Context, int) ([]int, error) {
			return nil, errors.New("plain failure")
		},
	}
	c := apitest.Serve(t, stub)
	ctx := context.Background()

	st, err := c.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "1.2.3", st.Version)
	require.Equal(t, 443, st.Tunnels[0].Ports[0].Listen)

	require.NoError(t, c.NodeRename(ctx, "de-1", "Germany"))
	require.Equal(t, []string{"de-1", "Germany"}, gotRename)

	e := requireCode(t, c.NodeRemove(ctx, "x-9"), deyerr.N008)
	require.Equal(t, "Unknown node: x-9", e.Message())
	require.NotEmpty(t, e.Fix())

	jc, err := c.NodeJoinCommand(ctx, 15*time.Minute)
	require.NoError(t, err)
	require.Equal(t, "15m0s", jc.Link)

	ev, err := c.Events(ctx, api.EventQuery{Tunnel: "main"})
	require.NoError(t, err)
	require.Equal(t, []state.Event{{Seq: 7, Tunnel: "main"}}, ev)

	nodes, err := c.NodeList(ctx)
	require.NoError(t, err)
	require.Nil(t, nodes)

	_, err = c.PortSuggest(ctx, 3)
	requireCode(t, err, deyerr.X000)

	// Methods without a function return DEY-X008 through the wire too.
	_, err = c.TunnelList(ctx)
	e = requireCode(t, err, deyerr.X008)
	require.Contains(t, e.Message(), "TunnelList")
}

// PortOpenFirewall carries the confirmed command to the daemon and the
// firewall state back; a refusal (DEY-P032) keeps its code and texts.
func TestLocalRoundTripPortOpenFirewall(t *testing.T) {
	var got api.PortOpenRequest
	stub := &apitest.Stub{PortOpenFirewallFn: func(_ context.Context, req api.PortOpenRequest) (api.PortOpenResult, error) {
		got = req
		if req.Command != "ufw allow 443/tcp" {
			return api.PortOpenResult{}, deyerr.New(deyerr.P032, deyerr.Params{"port": "443/tcp", "firewall": "ufw", "command": "ufw allow 443/tcp"})
		}
		return api.PortOpenResult{Port: req.Port, Proto: req.Proto, Ran: req.Command, By: "ufw", FirewallOpen: true, FirewallName: "nftables, ufw"}, nil
	}}
	c := apitest.Serve(t, stub)
	ctx := context.Background()
	res, err := c.PortOpenFirewall(ctx, api.PortOpenRequest{Port: 443, Proto: "tcp", Command: "ufw allow 443/tcp"})
	require.NoError(t, err)
	require.Equal(t, api.PortOpenRequest{Port: 443, Proto: "tcp", Command: "ufw allow 443/tcp"}, got)
	require.Equal(t, api.PortOpenResult{Port: 443, Proto: "tcp", Ran: "ufw allow 443/tcp", By: "ufw", FirewallOpen: true, FirewallName: "nftables, ufw"}, res)

	_, err = c.PortOpenFirewall(ctx, api.PortOpenRequest{Port: 443, Proto: "tcp", Command: "ufw allow 444/tcp"})
	e := requireCode(t, err, deyerr.P032)
	require.Contains(t, e.Why(), "ufw allow 443/tcp")
	require.Contains(t, e.Fix(), "deyroute port check 443/tcp --open")
}

func TestLocalRoundTripStreaming(t *testing.T) {
	stub := &apitest.Stub{
		TunnelAddFn: func(_ context.Context, req api.TunnelAddRequest, progress func(api.Step)) (api.TunnelInfo, error) {
			progress(api.Step{ID: "install_hub", Title: "install backend on hub", Status: api.StepRunning})
			progress(api.Step{ID: "install_hub", Status: api.StepOK})
			return api.TunnelInfo{ID: req.ID, Name: req.Name}, nil
		},
		TunnelDeleteFn: func(_ context.Context, id string, progress func(api.Step)) error {
			progress(api.Step{ID: "stop", Status: api.StepFailed, Error: api.ToDTO(deyerr.New(deyerr.X007, deyerr.Params{"command": "systemctl"}))})
			return deyerr.New(deyerr.X007, deyerr.Params{"command": "systemctl"})
		},
		LogsFn: func(_ context.Context, q api.LogQuery, emit func(api.LogLine) error) error {
			for _, l := range []string{"a", "b", "c"} {
				if err := emit(api.LogLine{Source: q.Target, Line: l}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	c := apitest.Serve(t, stub)
	ctx := context.Background()

	var steps []api.Step
	info, err := c.TunnelAdd(ctx, api.TunnelAddRequest{ID: "main", Name: "Main"}, func(s api.Step) { steps = append(steps, s) })
	require.NoError(t, err)
	require.Equal(t, "main", info.ID)
	require.Len(t, steps, 2)
	require.Equal(t, api.StepOK, steps[1].Status)

	// nil callback is allowed.
	_, err = c.TunnelAdd(ctx, api.TunnelAddRequest{ID: "x"}, nil)
	require.NoError(t, err)

	var failed []api.Step
	err = c.TunnelDelete(ctx, "main", func(s api.Step) { failed = append(failed, s) })
	requireCode(t, err, deyerr.X007)
	require.Len(t, failed, 1)
	require.Equal(t, "DEY-X007", failed[0].Error.Code)

	var lines []string
	require.NoError(t, c.Logs(ctx, api.LogQuery{Target: "hub"}, func(l api.LogLine) error {
		lines = append(lines, l.Source+":"+l.Line)
		return nil
	}))
	require.Equal(t, []string{"hub:a", "hub:b", "hub:c"}, lines)
}

func TestLocalStreamingCancellation(t *testing.T) {
	serverDone := make(chan error, 1)
	stub := &apitest.Stub{
		LogsFn: func(ctx context.Context, _ api.LogQuery, emit func(api.LogLine) error) error {
			if err := emit(api.LogLine{Line: "first"}); err != nil {
				return err
			}
			<-ctx.Done()
			serverDone <- ctx.Err()
			return ctx.Err()
		},
	}
	c := apitest.Serve(t, stub)

	// Client context cancellation aborts the request; the server sees it.
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan string, 1)
	errc := make(chan error, 1)
	go func() {
		errc <- c.Logs(ctx, api.LogQuery{Follow: true}, func(l api.LogLine) error {
			got <- l.Line
			return nil
		})
	}()
	require.Equal(t, "first", <-got)
	cancel()
	requireCode(t, <-errc, deyerr.X042)
	select {
	case err := <-serverDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not see the cancellation")
	}

	// An emit error ends the call and is returned as is.
	stop := errors.New("stdout closed")
	err := c.Logs(context.Background(), api.LogQuery{Follow: true}, func(api.LogLine) error { return stop })
	require.ErrorIs(t, err, stop)
	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not see the disconnect")
	}
}

func TestLocalCallTimeout(t *testing.T) {
	stub := &apitest.Stub{
		StatusFn: func(ctx context.Context) (api.Status, error) {
			<-ctx.Done()
			return api.Status{}, ctx.Err()
		},
	}
	sock := serve(t, stub)
	c, err := api.Dial(sock, api.DialOptions{Timeout: 100 * time.Millisecond, Service: "deyroute-node"})
	require.NoError(t, err)
	start := time.Now()
	_, err = c.Status(context.Background())
	e := requireCode(t, err, deyerr.X042)
	require.Contains(t, e.Fix(), "deyroute-node")
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestLocalPanicRecovered(t *testing.T) {
	stub := &apitest.Stub{
		StatusFn: func(context.Context) (api.Status, error) { panic("boom") },
		ConfigApplyFn: func(context.Context, func(api.Step)) (api.ApplyResult, error) {
			panic("streaming boom")
		},
		OptimizeStatusFn: func(context.Context) (api.OptimizeStatus, error) { return api.OptimizeStatus{Profile: "balanced"}, nil },
	}
	c := apitest.Serve(t, stub)
	_, err := c.Status(context.Background())
	requireCode(t, err, deyerr.X000)
	_, err = c.ConfigApply(context.Background(), nil)
	requireCode(t, err, deyerr.X000)
	// The server keeps working.
	os, err := c.OptimizeStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, "balanced", os.Profile)
}

func TestDialMissingSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "none.sock")
	_, err := api.Dial(sock, api.DialOptions{})
	e := requireCode(t, err, deyerr.X003)
	require.Equal(t, "systemctl start deyroute-hub", e.Fix())
	require.Equal(t, deyerr.ExitSystem, e.ExitCode())

	_, err = api.Dial(sock, api.DialOptions{Service: "deyroute-node"})
	e = requireCode(t, err, deyerr.X003)
	require.Equal(t, "systemctl start deyroute-node", e.Fix())

	// A stale socket file (nobody listening) is also X003.
	stale := filepath.Join(t.TempDir(), "stale.sock")
	ln, err := net.Listen("unix", stale)
	require.NoError(t, err)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, ln.Close())
	_, err = api.Dial(stale, api.DialOptions{})
	requireCode(t, err, deyerr.X003)
}

func TestDaemonStopsBetweenCalls(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "d.sock")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	stub := &apitest.Stub{StatusFn: func(context.Context) (api.Status, error) { return api.Status{Role: "node"}, nil }}
	go func() {
		done <- api.ServeLocalReady(ctx, sock, api.NewLocalHandler(stub, nil), nil, func() { close(ready) })
	}()
	<-ready
	c, err := api.Dial(sock, api.DialOptions{})
	require.NoError(t, err)
	_, err = c.Status(context.Background())
	require.NoError(t, err)
	cancel()
	require.NoError(t, <-done)
	_, err = c.Status(context.Background())
	requireCode(t, err, deyerr.X003)
	_, err = c.ConfigApply(context.Background(), nil)
	requireCode(t, err, deyerr.X003)
}

func TestServeLocalSocketHandling(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run", "deyroute")
	sock := filepath.Join(dir, "daemon.sock")

	// A stale socket is replaced.
	require.NoError(t, os.MkdirAll(dir, 0o700))
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, ln.Close())

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- api.ServeLocalReady(ctx, sock, api.NewLocalHandler(&apitest.Stub{}, nil), nil, func() { close(ready) })
	}()
	<-ready
	fi, err := os.Stat(sock)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	di, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), di.Mode().Perm())

	// A second daemon on the same live socket is refused.
	err = api.ServeLocal(context.Background(), sock, http.NotFoundHandler(), nil)
	requireCode(t, err, deyerr.X040)

	cancel()
	require.NoError(t, <-done)

	// A regular file at the socket path is never removed.
	require.NoError(t, os.WriteFile(sock, []byte("x"), 0o600))
	err = api.ServeLocal(context.Background(), sock, http.NotFoundHandler(), nil)
	requireCode(t, err, deyerr.X041)
	data, err := os.ReadFile(sock)
	require.NoError(t, err)
	require.Equal(t, "x", string(data))
}

// rawPost calls the socket without the generated client.
func rawPost(t *testing.T, sock, method, path, body string) (*http.Response, map[string]json.RawMessage) {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
		DisableKeepAlives: true,
	}}
	req, err := http.NewRequest(method, "http://deyroute"+path, strings.NewReader(body))
	require.NoError(t, err)
	resp, err := hc.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, err = buf.ReadFrom(resp.Body)
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m), buf.String())
	return resp, m
}

func TestLocalProtocolErrors(t *testing.T) {
	var mu sync.Mutex
	var renamed []string
	sock := serve(t, &apitest.Stub{NodeRenameFn: func(_ context.Context, id, name string) error {
		mu.Lock()
		renamed = append(renamed, id, name)
		mu.Unlock()
		return nil
	}})
	cases := []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPost, "/v1/rpc/Nope", "[]", http.StatusNotFound},
		{http.MethodPost, "/other", "[]", http.StatusNotFound},
		{http.MethodGet, "/v1/rpc/NodeRename", "", http.StatusMethodNotAllowed},
		{http.MethodPost, "/v1/rpc/NodeRename", `["only-one"]`, http.StatusBadRequest},
		{http.MethodPost, "/v1/rpc/NodeRename", `{"not":"array"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		resp, m := rawPost(t, sock, tc.method, tc.path, tc.body)
		require.Equal(t, tc.status, resp.StatusCode, tc.path)
		var dto api.ErrorDTO
		require.NoError(t, json.Unmarshal(m["error"], &dto))
		require.Equal(t, "DEY-X006", dto.Code)
	}
	// A wrongly typed argument is reported in the body.
	_, m := rawPost(t, sock, http.MethodPost, "/v1/rpc/NodeRename", `[1, 2]`)
	var dto api.ErrorDTO
	require.NoError(t, json.Unmarshal(m["error"], &dto))
	require.Equal(t, "DEY-X006", dto.Code)

	resp, m := rawPost(t, sock, http.MethodPost, "/v1/rpc/NodeRename", `["a","b"]`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "null", string(m["result"]))
	require.Equal(t, []string{"a", "b"}, renamed)
}
