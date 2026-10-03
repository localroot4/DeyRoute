package front

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"
)

// FanIn merges the accept streams of several listeners into one
// net.Listener (the direct control listener and the front's). A failure of
// one input never stops the others: that input is dropped and the rest keep
// delivering. Accept returns an error only when Close was called
// (net.ErrClosed) or when every input has failed (an error that wraps the
// last failure). Close closes every input and every connection that was
// accepted but not handed out. Addr is the address of the first listener.
func FanIn(listeners ...net.Listener) net.Listener {
	f := &fanIn{
		ls:   append([]net.Listener(nil), listeners...),
		out:  make(chan net.Conn),
		done: make(chan struct{}),
		dead: make(chan struct{}),
	}
	f.alive = len(f.ls)
	if f.alive == 0 {
		f.lastErr = errors.New("front: no listeners")
		close(f.dead)
	}
	for _, l := range f.ls {
		f.wg.Add(1)
		go f.run(l)
	}
	return f
}

type fanIn struct {
	ls   []net.Listener
	out  chan net.Conn
	done chan struct{}
	dead chan struct{} // closed when every input has failed
	wg   sync.WaitGroup

	mu      sync.Mutex
	alive   int
	lastErr error
	closing bool

	closeOnce sync.Once
	closeErr  error
}

func (f *fanIn) run(l net.Listener) {
	defer f.wg.Done()
	var delay time.Duration
	for {
		c, err := l.Accept()
		if err != nil {
			if f.isClosing() {
				return
			}
			if isTemporary(err) {
				delay = backoff(delay)
				select {
				case <-time.After(delay):
					continue
				case <-f.done:
					return
				}
			}
			f.inputFailed(err)
			return
		}
		delay = 0
		select {
		case f.out <- c:
		case <-f.done:
			_ = c.Close()
			return
		}
	}
}

func (f *fanIn) isClosing() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closing
}

func (f *fanIn) inputFailed(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alive--
	f.lastErr = err
	if f.alive == 0 && !f.closing {
		close(f.dead)
	}
}

// Accept returns the next connection of any input.
func (f *fanIn) Accept() (net.Conn, error) {
	select {
	case <-f.done:
		return nil, net.ErrClosed
	default:
	}
	select {
	case c := <-f.out:
		return c, nil
	case <-f.done:
		return nil, net.ErrClosed
	case <-f.dead:
		f.mu.Lock()
		err := f.lastErr
		f.mu.Unlock()
		return nil, fmt.Errorf("front: every listener failed: %w", err)
	}
}

// Close closes every input and waits for the accept goroutines. It is safe
// to call more than once.
func (f *fanIn) Close() error {
	f.closeOnce.Do(func() {
		f.mu.Lock()
		f.closing = true
		f.mu.Unlock()
		close(f.done)
		var errs []error
		for _, l := range f.ls {
			if err := l.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				errs = append(errs, err)
			}
		}
		f.closeErr = errors.Join(errs...)
	})
	f.wg.Wait()
	return f.closeErr
}

// Addr returns the address of the first listener.
func (f *fanIn) Addr() net.Addr {
	if len(f.ls) == 0 {
		return &net.TCPAddr{}
	}
	return f.ls[0].Addr()
}

// isTemporary reports whether an Accept error is worth retrying: a timeout,
// resource exhaustion or an aborted handshake. Anything else (a closed or
// broken listener) is final.
func isTemporary(err error) bool {
	if errors.Is(err, net.ErrClosed) {
		return false
	}
	if errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) || errors.Is(err, syscall.ENOBUFS) ||
		errors.Is(err, syscall.ENOMEM) || errors.Is(err, syscall.ECONNABORTED) {
		return true
	}
	var t interface{ Temporary() bool }
	if errors.As(err, &t) && t.Temporary() {
		return true
	}
	return isTimeoutErr(err)
}

// backoff doubles d from 5 ms up to 1 s.
func backoff(d time.Duration) time.Duration {
	if d == 0 {
		return 5 * time.Millisecond
	}
	return min(2*d, time.Second)
}
