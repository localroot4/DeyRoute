package systemd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeLog(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.log")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func TestLogTailBasics(t *testing.T) {
	_, err := LogTail(filepath.Join(t.TempDir(), "missing.log"), 40)
	require.ErrorIs(t, err, fs.ErrNotExist)

	lines, err := LogTail(writeLog(t, "a\n"), 0)
	require.NoError(t, err)
	require.Nil(t, lines)

	lines, err = LogTail(writeLog(t, ""), 40)
	require.NoError(t, err)
	require.Empty(t, lines)

	lines, err = LogTail(writeLog(t, "a\nb\nc\n"), 40)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c"}, lines)

	lines, err = LogTail(writeLog(t, "a\nb\nc"), 2)
	require.NoError(t, err)
	require.Equal(t, []string{"b", "c"}, lines)

	lines, err = LogTail(writeLog(t, "a\r\nb\r\n\n"), 2)
	require.NoError(t, err)
	require.Equal(t, []string{"b", ""}, lines)

	lines, err = LogTail(writeLog(t, "only"), 1)
	require.NoError(t, err)
	require.Equal(t, []string{"only"}, lines)

	_, err = LogTail(t.TempDir(), 3) // a directory
	require.Error(t, err)
}

func TestLogTailLargeFile(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "line %05d %s\n", i, strings.Repeat("x", i%50))
	}
	p := writeLog(t, b.String())
	lines, err := LogTail(p, FailureLogLines)
	require.NoError(t, err)
	require.Len(t, lines, FailureLogLines)
	require.True(t, strings.HasPrefix(lines[0], "line 19960 "), lines[0])
	require.True(t, strings.HasPrefix(lines[39], "line 19999 "), lines[39])

	// Exactly at a chunk boundary.
	var c strings.Builder
	line := strings.Repeat("y", 1023) + "\n" // 1 KiB lines → 8 per chunk
	for i := 0; i < 64; i++ {
		c.WriteString(line)
	}
	lines, err = LogTail(writeLog(t, c.String()), 8)
	require.NoError(t, err)
	require.Len(t, lines, 8)
	for _, l := range lines {
		require.Len(t, l, 1023)
	}
}

func TestLogTailHugeLine(t *testing.T) {
	content := strings.Repeat("z", maxTailScan+tailChunk*3) + "\nlast\n"
	lines, err := LogTail(writeLog(t, content), 40)
	require.NoError(t, err)
	require.Equal(t, []string{"last"}, lines)
}

func TestLogTailEdgeCases(t *testing.T) {
	// A lone newline is one empty line (like tail -n).
	lines, err := LogTail(writeLog(t, "\n"), 5)
	require.NoError(t, err)
	require.Equal(t, []string{""}, lines)

	// The scan window stops exactly at a line start (the byte before it is a
	// newline): that first line is complete and must be kept.
	head := strings.Repeat("h", maxTailScan-1) + "\n" // ends right before the window
	body := strings.Repeat("b", tailChunk-len("\nend\n")) + "\nend\n"
	// Pad so the window [size-maxTailScan, size) starts right after head.
	content := head + strings.Repeat("p", maxTailScan-len(body)-1) + "\n" + body
	lines, err = LogTail(writeLog(t, content), 1000)
	require.NoError(t, err)
	require.Equal(t, "end", lines[len(lines)-1])
	require.Len(t, lines, 3, "the complete line at the window start was dropped")
	require.Len(t, lines[0], maxTailScan-len(body)-1)
}

// Backend logs may print tokens; LogTail output reaches owner-visible DEY
// errors, so it goes through the central secret filter.
func TestLogTailRedacts(t *testing.T) {
	p := writeLog(t, "starting\ntoken = s3cr3t-backhaul-token-value\nPOST https://api.telegram.org/bot123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi/sendMessage\n")
	lines, err := LogTail(p, 3)
	require.NoError(t, err)
	joined := strings.Join(lines, "\n")
	require.NotContains(t, joined, "s3cr3t-backhaul-token-value")
	require.NotContains(t, joined, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi")
	require.Equal(t, "starting", lines[0])
	require.Contains(t, joined, "***")
}
