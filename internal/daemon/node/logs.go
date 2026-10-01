package node

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/systemd"
)

// Log limits.
const (
	// DefaultLogLines is the backlog of logs.tail / Logs when none is asked.
	DefaultLogLines = 200
	// MaxLogLines caps the backlog.
	MaxLogLines = 10000
	// LogTargetNode names the agent's own log (node.log).
	LogTargetNode = "node"
	// maxFollowChunk bounds what one poll of a followed log reads.
	maxFollowChunk = 1 << 20
	// maxFollowLine bounds a line kept across polls without a newline.
	maxFollowLine = 64 << 10
)

// logPath resolves a log target to its file: "node" (or empty) is
// node.log, a tunnel id is tunnels/<id>.log.
func (a *agent) logPath(command, target string) (string, error) {
	switch {
	case target == "" || target == LogTargetNode:
		return a.path(LogFile), nil
	case config.ValidID(target):
		return a.path(systemd.TunnelLogFile(target)), nil
	}
	return "", a.refuse(command, fmt.Sprintf("unknown log target %q (node or a tunnel id)", target))
}

// readTail returns the last n (redacted) lines of path, keeping only lines
// at or after since when they carry a JSON "ts" field, and the file size
// the tail was read at (where a follow continues). A missing file has no
// lines.
func readTail(path string, n int, since time.Time) ([]string, int64, error) {
	if n <= 0 {
		n = DefaultLogLines
	}
	n = min(n, MaxLogLines)
	fi, err := os.Stat(path)
	if stderrors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, deyerr.Wrap(deyerr.X022, err, deyerr.Params{"path": path})
	}
	lines, err := systemd.LogTail(path, n)
	if err != nil && !stderrors.Is(err, fs.ErrNotExist) {
		return nil, 0, deyerr.Wrap(deyerr.X022, err, deyerr.Params{"path": path})
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
	return lines, fi.Size(), nil
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

// follow emits (redacted) lines appended to path after offset until ctx
// ends or emit fails. A rotated or truncated file is read again from its
// start.
func (a *agent) follow(ctx context.Context, path string, offset int64, emit func([]string) error) {
	t := time.NewTicker(a.o.FollowPoll)
	defer t.Stop()
	var prev os.FileInfo
	var partial []byte
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
		} else {
			prev = nil
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// readAt reads n bytes of path from off.
func readAt(path string, off, n int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- log file chosen by logPath
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

// splitLines returns the complete, redacted lines of data and the
// incomplete rest (dropped once it grows beyond maxFollowLine).
func splitLines(data []byte) ([]string, []byte) {
	var lines []string
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, dlog.Redact(strings.TrimRight(string(data[:i]), "\r")))
		data = data[i+1:]
	}
	if len(data) > maxFollowLine {
		lines = append(lines, dlog.Redact(string(data)))
		data = nil
	}
	return lines, append([]byte(nil), data...)
}

// logsTail answers logs.tail: the backlog as the result, or with Follow the
// backlog and every new line streamed to the hub until the command ends.
func (a *agent) logsTail(ctx context.Context, args api.LogsArgs, stream func([]string)) ([]string, error) {
	p, err := a.logPath(api.CmdLogsTail, args.Target)
	if err != nil {
		return nil, err
	}
	lines, size, err := readTail(p, args.Lines, args.Since)
	if err != nil {
		return nil, err
	}
	if !args.Follow {
		if lines == nil {
			lines = []string{}
		}
		return lines, nil
	}
	if len(lines) > 0 {
		stream(lines)
	}
	a.follow(ctx, p, size, func(l []string) error {
		stream(l)
		return ctx.Err()
	})
	return []string{}, nil
}

// localLogs implements Local.Logs on the node.
func (a *agent) localLogs(ctx context.Context, q api.LogQuery, emit func(api.LogLine) error) error {
	if q.Target == "hub" {
		return deyerr.New(deyerr.X009, deyerr.Params{"role": config.RoleNode, "need": config.RoleHub})
	}
	p, err := a.logPath("logs", q.Target)
	if err != nil {
		return err
	}
	lines, size, err := readTail(p, q.Lines, q.Since)
	if err != nil {
		return err
	}
	send := func(ls []string) error {
		for _, l := range ls {
			if err := emit(api.LogLine{Source: LogTargetNode, Line: l}); err != nil {
				return err
			}
		}
		return nil
	}
	if err := send(lines); err != nil || !q.Follow {
		return nil
	}
	// A follow ends with the client, or when the agent stops.
	fctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(a.ctx, cancel)
	defer stop()
	a.follow(fctx, p, size, send)
	return nil
}
