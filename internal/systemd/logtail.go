package systemd

import (
	"bytes"
	"io"
	"os"
	"strings"

	deylog "github.com/localroot4/deyroute/internal/log"
)

// Log tail limits.
const (
	// FailureLogLines is how many backend log lines accompany a start
	// failure (section 7: "40 last log lines").
	FailureLogLines = 40
	tailChunk       = 8 << 10
	// maxTailScan bounds how far back LogTail reads (a log with gigantic
	// lines returns what fits).
	maxTailScan = 4 << 20
)

// LogTail returns the last n lines of the file at path (oldest first,
// without line terminators). It reads backwards in chunks, so the cost is
// proportional to the tail, not to the file size; at most 4 MiB are read,
// and a line that starts before that window is left out rather than
// returned truncated.
//
// The lines are passed through the central secret filter (internal/log
// Redact): they end up in owner-visible DEY errors and doctor output, where
// section 11 forbids tokens and keys, and backend logs may print them.
//
// A missing file returns an error matching fs.ErrNotExist; an empty file
// returns no lines.
func LogTail(path string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	f, err := os.Open(path) // #nosec G304 -- log path chosen by deyroute
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}

	off := fi.Size()
	var chunks [][]byte // newest first
	newlines, scanned := 0, 0
	// n complete lines need n+1 newlines unless we reach the file start
	// (the trailing newline of the last line counts as one).
	for off > 0 && newlines <= n && scanned < maxTailScan {
		size := int64(tailChunk)
		if off < size {
			size = off
		}
		off -= size
		chunk := make([]byte, size)
		if _, err := f.ReadAt(chunk, off); err != nil && err != io.EOF {
			return nil, err
		}
		newlines += bytes.Count(chunk, []byte{'\n'})
		scanned += len(chunk)
		chunks = append(chunks, chunk)
	}
	if scanned == 0 {
		return []string{}, nil
	}
	// The first scanned line is complete only when the window starts at
	// the beginning of the file or right after a newline.
	firstComplete := off == 0
	if !firstComplete {
		prev := make([]byte, 1)
		if _, err := f.ReadAt(prev, off-1); err != nil && err != io.EOF {
			return nil, err
		}
		firstComplete = prev[0] == '\n'
	}

	var tail strings.Builder
	tail.Grow(scanned)
	for i := len(chunks) - 1; i >= 0; i-- {
		tail.Write(chunks[i])
	}
	lines := strings.Split(strings.TrimSuffix(tail.String(), "\n"), "\n")
	if !firstComplete {
		lines = lines[1:]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i, l := range lines {
		lines[i] = deylog.Redact(strings.TrimSuffix(l, "\r"))
	}
	return lines, nil
}
