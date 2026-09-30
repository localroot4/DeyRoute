package hub

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/systemd"
)

// Log streaming limits.
const (
	// DefaultLogLines is the backlog of Logs when none is asked.
	DefaultLogLines = 200
	// MaxLogLines caps the backlog.
	MaxLogLines = 10000
	// DefaultFollowPoll is the poll interval of a followed log file.
	DefaultFollowPoll = 500 * time.Millisecond
	// Log targets besides tunnel ids.
	LogTargetHub  = "hub"
	LogTargetNode = "node"
	// Sources of api.LogLine ([hub]/[node] prefixes in the CLI).
	SourceHub  = "hub"
	SourceNode = "node"
	// maxFollowChunk bounds what one poll of a followed log reads.
	maxFollowChunk = 1 << 20
	// maxFollowLine bounds a line kept across polls without a newline.
	maxFollowLine = 64 << 10
)

// logSink serialises emit calls from the hub and node streams and stops
// every stream once the client went away.
type logSink struct {
	mu     sync.Mutex
	emit   func(api.LogLine) error
	failed bool
	cancel context.CancelFunc
}

// send emits lines of source (redacted); an emit error cancels the call.
func (s *logSink) send(source string, lines []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return errStreamGone
	}
	for _, l := range lines {
		if err := s.emit(api.LogLine{Source: source, Line: dlog.Redact(l)}); err != nil {
			s.failed = true
			if s.cancel != nil {
				s.cancel()
			}
			return err
		}
	}
	return nil
}

// errStreamGone ends a log stream whose client disconnected.
var errStreamGone = deyerr.Plain("hub: log client went away")

// Logs implements api.Local (sections 13 and 14): target "hub" (or empty)
// is hub.log; a tunnel id is the tunnel's log on the hub
// (/var/log/deyroute/tunnels/<id>.log) together with the same log on the
// tunnel's active (else primary) node, streamed over the control channel
// (logs.tail); every line carries its Source ("hub" / "node"). With Follow
// new lines are streamed until ctx ends or the client disconnects.
func (l *local) Logs(ctx context.Context, q api.LogQuery, emit func(api.LogLine) error) error {
	h := l.h
	target := strings.TrimSpace(q.Target)
	n := q.Lines
	if n <= 0 {
		n = DefaultLogLines
	}
	n = min(n, MaxLogLines)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sink := &logSink{emit: emit, cancel: cancel}
	switch target {
	case "", LogTargetHub:
		h.logFile(ctx, h.path(LogFile), n, q.Since, q.Follow, SourceHub, sink)
		return nil
	case LogTargetNode:
		return withLog(deyerr.New(deyerr.X009, deyerr.Params{"role": config.RoleHub, "need": config.RoleNode}))
	}
	cfg := h.Config()
	t, ok := cfg.Tunnel(target)
	if !ok {
		return withLog(deyerr.New(deyerr.C021, deyerr.Params{"tunnel": target}))
	}
	node := ""
	if ts, known := h.tunnelState(t.ID); known && ts.Active.Node != "" {
		node = ts.Active.Node
	} else if len(t.Nodes) > 0 {
		node = t.Nodes[0]
	}
	hubFile := h.path(systemd.TunnelLogFile(t.ID))
	args := api.LogsArgs{Target: t.ID, Lines: n, Since: q.Since, Follow: q.Follow}
	if !q.Follow {
		h.logFile(ctx, hubFile, n, q.Since, false, SourceHub, sink)
		if node != "" {
			var lines []string
			if err := h.Call(ctx, node, api.CmdLogsTail, args, &lines); err != nil {
				_ = sink.send(SourceNode, []string{deyerr.As(err).Error()})
			} else {
				_ = sink.send(SourceNode, lines)
			}
		}
		return nil
	}
	var wg sync.WaitGroup
	if node != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := h.Stream(ctx, node, api.CmdLogsTail, args, func(lines []string) { _ = sink.send(SourceNode, lines) })
			if err != nil && ctx.Err() == nil {
				_ = sink.send(SourceNode, []string{deyerr.As(err).Error()})
			}
		}()
	}
	h.logFile(ctx, hubFile, n, q.Since, true, SourceHub, sink)
	cancel()
	wg.Wait()
	return nil
}

// logFile sends the last n lines of path (only lines at or after since when
// they carry a JSON "ts") and, with follow, every line appended later until
// ctx ends. A missing file has no lines (it may appear later).
func (h *Hub) logFile(ctx context.Context, path string, n int, since time.Time, follow bool, source string, sink *logSink) {
	lines, size := readTail(path, n, since)
	if err := sink.send(source, lines); err != nil || !follow {
		return
	}
	h.follow(ctx, path, size, func(ls []string) error { return sink.send(source, ls) })
}

// readTail returns the last n lines of path filtered by since, and the
// file size the tail was read at (where a follow continues).
func readTail(path string, n int, since time.Time) ([]string, int64) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, 0
	}
	lines, err := systemd.LogTail(path, n)
	if err != nil {
		return nil, fi.Size()
	}
	if !since.IsZero() {
		kept := lines[:0]
		for _, l := range lines {
			if ts, ok := lineTime(l); !ok || !ts.Before(since) {
				kept = append(kept, l)
			}
		}
		lines = kept
	}
	return lines, fi.Size()
}

// lineTime extracts the "ts" field of a JSON log line.
func lineTime(line string) (time.Time, bool) {
	if !strings.HasPrefix(line, "{") {
		return time.Time{}, false
	}
	var v struct {
		TS time.Time `json:"ts"`
	}
	if err := json.Unmarshal([]byte(line), &v); err != nil || v.TS.IsZero() {
		return time.Time{}, false
	}
	return v.TS, true
}

// follow emits the lines appended to path after offset until ctx ends or
// emit fails. A rotated or truncated file is read again from its start.
func (h *Hub) follow(ctx context.Context, path string, offset int64, emit func([]string) error) {
	poll := time.NewTicker(h.o.FollowPoll)
	defer poll.Stop()
	var (
		prev    os.FileInfo
		partial []byte
	)
	for {
		if fi, err := os.Stat(path); err == nil {
			if fi.Size() < offset || (prev != nil && !os.SameFile(prev, fi)) {
				offset, partial = 0, nil
			}
			prev = fi
			if fi.Size() > offset {
				chunk, err := readAt(path, offset, min(fi.Size()-offset, maxFollowChunk))
				if err == nil {
					offset += int64(len(chunk))
					var lines []string
					lines, partial = splitLines(append(partial, chunk...))
					if len(lines) > 0 {
						if err := emit(lines); err != nil {
							return
						}
					}
				}
			}
		} else if stderrors.Is(err, fs.ErrNotExist) {
			prev, offset, partial = nil, 0, nil
		}
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
		}
	}
}

// readAt reads up to n bytes of path from off.
func readAt(path string, off, n int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- a deyroute log file chosen by Logs
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, n)
	m, err := f.ReadAt(buf, off)
	if err != nil && !stderrors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:m], nil
}

// splitLines returns the complete lines of data and the incomplete rest
// (emitted anyway once it grows beyond maxFollowLine).
func splitLines(data []byte) ([]string, []byte) {
	var lines []string
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, strings.TrimRight(string(data[:i]), "\r"))
		data = data[i+1:]
	}
	if len(data) > maxFollowLine {
		lines = append(lines, string(data))
		data = nil
	}
	return lines, append([]byte(nil), data...)
}
