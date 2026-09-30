package health

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPoolBoundsConcurrency(t *testing.T) {
	p := NewPool(0)
	assert.Equal(t, PoolSize, p.Size())
	var running, peak, total atomic.Int32
	for i := 0; i < 50; i++ {
		require.NoError(t, p.Go(context.Background(), func() {
			n := running.Add(1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			running.Add(-1)
			total.Add(1)
		}))
	}
	p.Close()
	assert.Equal(t, int32(50), total.Load(), "Close waits for every task")
	assert.LessOrEqual(t, peak.Load(), int32(PoolSize))
	assert.Positive(t, peak.Load())
}

func TestPoolGoBlocksAndHonoursContext(t *testing.T) {
	p := NewPool(1)
	release := make(chan struct{})
	require.NoError(t, p.Go(context.Background(), func() { <-release }))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := p.Go(ctx, func() { t.Error("must not run") })
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.GreaterOrEqual(t, time.Since(start), 40*time.Millisecond)

	close(release)
	p.Close()
}

func TestPoolGoDoneContextNeverRuns(t *testing.T) {
	// A free slot and a done ctx are both ready: fn must never run.
	p := NewPool(8)
	defer p.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var ran atomic.Int32
	for i := 0; i < 200; i++ {
		assert.ErrorIs(t, p.Go(ctx, func() { ran.Add(1) }), context.Canceled)
	}
	p.Close()
	assert.Zero(t, ran.Load())
}

func TestPoolCloseReleasesWaiters(t *testing.T) {
	p := NewPool(1)
	release := make(chan struct{})
	require.NoError(t, p.Go(context.Background(), func() { <-release }))
	errs := make(chan error, 1)
	go func() { errs <- p.Go(context.Background(), func() {}) }()
	time.Sleep(20 * time.Millisecond)

	closed := make(chan struct{})
	go func() { p.Close(); close(closed) }()
	select {
	case err := <-errs:
		assert.ErrorIs(t, err, ErrPoolClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Go was not released by Close")
	}
	select {
	case <-closed:
		t.Fatal("Close returned before the running task finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-closed
	assert.ErrorIs(t, p.Go(context.Background(), func() {}), ErrPoolClosed)
	p.Close() // idempotent
}

func TestPoolConcurrentGoAndClose(t *testing.T) {
	for i := 0; i < 20; i++ {
		p := NewPool(2)
		var wg sync.WaitGroup
		for j := 0; j < 8; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = p.Go(context.Background(), func() { time.Sleep(time.Millisecond) })
			}()
		}
		p.Close()
		wg.Wait()
		p.Close()
	}
}

func TestPathAll(t *testing.T) {
	p := NewPool(4)
	defer p.Close()
	good := bannerServer(t)
	bad := closedPort(t)
	targets := []Target{
		{Addr: good, Kind: KindAuto},
		{Addr: bad, Kind: KindTCP},
		{Addr: good, Kind: KindTCP},
		{Addr: "nope", Kind: KindTCP},
	}
	res := PathAll(context.Background(), p, targets, time.Second)
	require.Len(t, res, 4)
	assert.True(t, res[0].OK, res[0].Err)
	assert.Equal(t, ReasonRefused, res[1].Err)
	assert.True(t, res[2].OK, res[2].Err)
	assert.Equal(t, ReasonBadAddress, res[3].Err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	busy := NewPool(1)
	release := make(chan struct{})
	require.NoError(t, busy.Go(context.Background(), func() { <-release }))
	res = PathAll(ctx, busy, targets, time.Second)
	for _, r := range res {
		assert.False(t, r.OK)
		assert.Equal(t, ReasonCanceled, r.Err)
	}
	close(release)
	busy.Close()

	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()
	busy = NewPool(1)
	release = make(chan struct{})
	require.NoError(t, busy.Go(context.Background(), func() { <-release }))
	res = PathAll(ctx, busy, targets[:1], time.Second)
	assert.Equal(t, ReasonTimeout, res[0].Err)
	close(release)
	busy.Close()
}
