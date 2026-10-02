package fronttest

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// CutProxy is a TCP proxy that silently black-holes one direction of every
// connection after a number of bytes: the bytes are read and thrown away, the
// connection stays open (a stalled path, not a reset). It is the byte-level
// counterpart of the CDN's CutAfterBytes knob, for tests that put the cut
// between two arbitrary peers.
type CutProxy struct {
	ln     net.Listener
	target string
	after  int64
	dir    Direction

	cutNow atomic.Bool

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
	wg     sync.WaitGroup
	once   sync.Once

	up, down atomic.Int64 // bytes forwarded, all connections
}

// NewCutProxy listens on listenAddr ("" = 127.0.0.1:0) and proxies to
// targetAddr. After afterBytes bytes in direction dir (Up = client to target,
// Down = target to client, Both = each) of a connection, that direction stops.
// afterBytes 0 cuts nothing until CutNow is called.
func NewCutProxy(listenAddr, targetAddr string, afterBytes int64, dir Direction) (*CutProxy, error) {
	if listenAddr == "" {
		listenAddr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}
	p := &CutProxy{ln: ln, target: targetAddr, after: afterBytes, dir: dir, conns: map[net.Conn]struct{}{}}
	p.wg.Add(1)
	go p.acceptLoop()
	return p, nil
}

// Addr returns the listen address.
func (p *CutProxy) Addr() string { return p.ln.Addr().String() }

// CutNow stops the configured direction(s) of every connection, running or
// new, right away.
func (p *CutProxy) CutNow() { p.cutNow.Store(true) }

// Bytes returns how many bytes were forwarded in dir since the start.
func (p *CutProxy) Bytes(dir Direction) int64 {
	if dir == Up {
		return p.up.Load()
	}
	return p.down.Load()
}

// Close stops the proxy, closes every connection and waits for its goroutines.
func (p *CutProxy) Close() error {
	var err error
	p.once.Do(func() {
		err = p.ln.Close()
		p.mu.Lock()
		p.closed = true
		for c := range p.conns {
			_ = c.Close()
		}
		p.mu.Unlock()
	})
	p.wg.Wait()
	return err
}

func (p *CutProxy) track(c net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		_ = c.Close()
		return false
	}
	p.conns[c] = struct{}{}
	return true
}

func (p *CutProxy) untrack(c net.Conn) {
	p.mu.Lock()
	delete(p.conns, c)
	p.mu.Unlock()
	_ = c.Close()
}

func (p *CutProxy) acceptLoop() {
	defer p.wg.Done()
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		if !p.track(c) {
			continue
		}
		p.wg.Add(1)
		go p.serve(c)
	}
}

func (p *CutProxy) serve(client net.Conn) {
	defer p.wg.Done()
	defer p.untrack(client)
	t, err := net.DialTimeout("tcp", p.target, 5*time.Second)
	if err != nil {
		return
	}
	if !p.track(t) {
		return
	}
	defer p.untrack(t)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); p.copy(Up, t, client) }()
	go func() { defer wg.Done(); p.copy(Down, client, t) }()
	wg.Wait()
}

func (p *CutProxy) copy(dir Direction, dst, src net.Conn) {
	buf := make([]byte, 16*1024)
	var sent int64
	counter := &p.down
	if dir == Up {
		counter = &p.up
	}
	cutting := p.dir == Both || p.dir == dir
	for {
		n, err := src.Read(buf)
		if n > 0 {
			out := buf[:n]
			if cutting {
				switch {
				case p.cutNow.Load():
					out = nil
				case p.after > 0:
					if rem := p.after - sent; rem <= 0 {
						out = nil
					} else if int64(len(out)) > rem {
						out = out[:rem]
					}
				}
			}
			if len(out) > 0 {
				if _, werr := dst.Write(out); werr != nil {
					_ = src.Close()
					_ = dst.Close()
					return
				}
				sent += int64(len(out))
				counter.Add(int64(len(out)))
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if cw, ok := dst.(closeWriter); ok {
					_ = cw.CloseWrite()
				}
			} else {
				_ = src.Close()
				_ = dst.Close()
			}
			return
		}
	}
}
