package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Cancelling a non-streaming call aborts the request and the daemon's
// implementation sees its context end.
func TestLocalNonStreamingCancellation(t *testing.T) {
	started := make(chan struct{})
	serverDone := make(chan error, 1)
	stub := &apitest.Stub{
		NodeTestFn: func(ctx context.Context, _ string) (api.NodeTestResult, error) {
			close(started)
			<-ctx.Done()
			serverDone <- ctx.Err()
			return api.NodeTestResult{}, ctx.Err()
		},
	}
	c := apitest.Serve(t, stub)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := c.NodeTest(ctx, "de-1")
		errc <- err
	}()
	<-started
	cancel()
	e := requireCode(t, <-errc, deyerr.X042)
	require.ErrorIs(t, e, context.Canceled)
	select {
	case err := <-serverDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon did not see the cancellation")
	}
}
