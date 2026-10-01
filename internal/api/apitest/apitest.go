// Package apitest provides test doubles for the Local API: Stub (generated
// from internal/api/local.go, one optional function per method) and Serve,
// which exposes any api.Local on a real unix socket so CLI and TUI tests
// exercise the same transport as production.
package apitest

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
)

// notImplemented is the answer of a Stub method without a function.
func notImplemented(method string) error {
	return deyerr.New(deyerr.X008, deyerr.Params{"feature": "api.Local." + method})
}

// Serve serves impl on a unix socket in a temporary directory for the
// duration of the test and returns a client connected to it (through
// api.Dial). The server stops during t.Cleanup.
func Serve(t testing.TB, impl api.Local) api.Local {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "d.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan struct{})
	h := api.NewLocalHandler(impl, dlog.Discard())
	go func() {
		done <- api.ServeLocalReady(ctx, sock, h, dlog.Discard(), func() { close(ready) })
	}()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("apitest: serve %s: %v", sock, err)
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatalf("apitest: serve %s: timeout", sock)
	}
	t.Cleanup(func() {
		cancel()
		<-done
	})
	c, err := api.Dial(sock, api.DialOptions{})
	if err != nil {
		t.Fatalf("apitest: dial %s: %v", sock, err)
	}
	return c
}
