package health

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrPoolClosed is returned by Pool.Go after Close.
var ErrPoolClosed = errors.New("health: probe pool closed")

// Pool is a bounded worker pool: at most Size functions run at once
// (section 12: probes run concurrently in a pool of 8).
type Pool struct {
	sem    chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// NewPool returns a pool running at most size functions concurrently
// (PoolSize when size <= 0).
func NewPool(size int) *Pool {
	if size <= 0 {
		size = PoolSize
	}
	return &Pool{sem: make(chan struct{}, size), done: make(chan struct{})}
}

// Size is the pool's concurrency limit.
func (p *Pool) Size() int { return cap(p.sem) }

// Go runs fn in the pool. It blocks while the pool is full and returns
// ctx's error when ctx is done first, or ErrPoolClosed after Close; in both
// cases fn is not run.
func (p *Pool) Go(ctx context.Context, fn func()) error {
	// select picks randomly among ready cases: check first so a done ctx or
	// a closed pool never runs fn even when a slot is free.
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-p.done:
		return ErrPoolClosed
	default:
	}
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return ErrPoolClosed
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		<-p.sem
		return ErrPoolClosed
	}
	p.wg.Add(1)
	p.mu.Unlock()
	go func() {
		defer func() {
			<-p.sem
			p.wg.Done()
		}()
		fn()
	}()
	return nil
}

// Close stops accepting work, releases callers blocked in Go and waits for
// running functions to return. It is idempotent.
func (p *Pool) Close() {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.done)
	}
	p.mu.Unlock()
	p.wg.Wait()
}

// Target is one probe of a batch run by PathAll.
type Target struct {
	Addr string
	Kind string
	Opts PathOptions
}

// PathAll probes every target through p (the "all ports every 60 s"
// report, `deyroute diag probe --all-ports`) and returns the results in the
// order of targets. Targets that could not be scheduled because ctx ended or
// the pool closed get a failed Result.
func PathAll(ctx context.Context, p *Pool, targets []Target, timeout time.Duration) []Result {
	out := make([]Result, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		err := p.Go(ctx, func() {
			defer wg.Done()
			out[i] = Path(ctx, t.Addr, t.Kind, timeout, t.Opts)
		})
		if err != nil {
			wg.Done()
			reason := ReasonCanceled
			if errors.Is(err, context.DeadlineExceeded) {
				reason = ReasonTimeout
			}
			for j := i; j < len(targets); j++ {
				out[j] = Result{Err: reason}
			}
			break
		}
	}
	wg.Wait()
	return out
}
