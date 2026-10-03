package wsconn

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// streamPeers pumps total bytes of a deterministic pattern from w to r and
// verifies them on the way out.
func pump(w io.Writer, r io.Reader, total, wchunk, rchunk int) error {
	var wg sync.WaitGroup
	werr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, wchunk)
		for off := 0; off < total; {
			n := min(wchunk, total-off)
			pattern(buf[:n], off)
			if _, err := w.Write(buf[:n]); err != nil {
				werr <- err
				return
			}
			off += n
		}
		werr <- nil
	}()
	buf := make([]byte, rchunk)
	want := make([]byte, rchunk)
	var rerr error
	for off := 0; off < total && rerr == nil; {
		n, err := r.Read(buf[:min(rchunk, total-off)])
		pattern(want[:n], off)
		if !bytes.Equal(buf[:n], want[:n]) {
			rerr = fmt.Errorf("data mismatch at offset %d", off)
			break
		}
		off += n
		rerr = err
	}
	if rerr != nil {
		return rerr
	}
	wg.Wait()
	return <-werr
}

func TestThroughput64MiBBothDirections(t *testing.T) {
	const total = 64 << 20
	cli, srv := connPair(t, Config{}, Config{}) // default 30 s pings: they must not get in the way
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := time.Now()
	for _, p := range []struct{ w, r *Conn }{{cli, srv}, {srv, cli}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A TLS-record-like write size and a different, odd read size.
			errs <- pump(p.w, p.r, total, 16<<10+5, 9000)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	t.Logf("2 x 64 MiB in %v", time.Since(start))
}

func TestThroughputOverTCPWithSlowReader(t *testing.T) {
	// A real TCP pair with a reader that stalls in the middle: the writer is
	// held back by backpressure, memory stays bounded (frames are streamed and
	// pooled, nothing accumulates) and the data arrives intact.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	acc := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		acc <- c
	}()
	rawC, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	cli := New(rawC, nil, Config{Client: true})
	srv := New(<-acc, nil, Config{})
	defer func() { _ = cli.Close(); _ = srv.Close() }()

	const total = 48 << 20
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	werr := make(chan error, 1)
	go func() {
		buf := make([]byte, 64<<10)
		for off := 0; off < total; off += len(buf) {
			pattern(buf, off)
			if _, err := cli.Write(buf); err != nil {
				werr <- err
				return
			}
		}
		werr <- nil
	}()

	buf := make([]byte, 32<<10)
	want := make([]byte, len(buf))
	var peak uint64
	for off := 0; off < total; {
		if off == 8<<20 || off == 24<<20 {
			time.Sleep(300 * time.Millisecond) // the slow consumer
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			peak = max(peak, m.HeapAlloc)
		}
		n, err := srv.Read(buf)
		require.NoError(t, err)
		pattern(want[:n], off)
		require.True(t, bytes.Equal(want[:n], buf[:n]), "mismatch at %d", off)
		off += n
	}
	require.NoError(t, <-werr)
	require.Less(t, peak, before.HeapAlloc+32<<20, "heap grew while the reader stalled")
}
