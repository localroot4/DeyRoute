package exec

// Before a node installs the deyroute binary of the signed release its hub
// sends (self.update, DR-03), it asks that binary whether the build checks
// signed updates itself: `<binary> --json version` must answer
// "signed_update": true. ReleaseVersion runs exactly that command line and
// nothing else, for a binary named deyroute; Runner never runs deyroute.
// It lives here because only this package may start programs (section 15).

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"io/fs"
	osexec "os/exec"
	"path/filepath"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// ReleaseBinaryName is the only base name ReleaseVersion runs.
const ReleaseBinaryName = "deyroute"

// MaxReleaseVersionOutput caps what the binary may print on stdout.
const MaxReleaseVersionOutput = 64 << 10

// releaseVersionTimeout bounds one run (a variable for the tests).
var releaseVersionTimeout = 15 * time.Second

// releaseVersionArgs is the whole command line after the program.
var releaseVersionArgs = []string{"--json", "version"}

// releaseVersionEnv is the whole environment of the binary: nothing of the
// caller's environment reaches it.
var releaseVersionEnv = []string{"PATH=" + defaultPath, "LC_ALL=C"}

// errTooMuchOutput stops reading a binary that prints more than
// MaxReleaseVersionOutput bytes.
var errTooMuchOutput = fmt.Errorf("more than %d bytes of output", MaxReleaseVersionOutput)

// ReleaseVersion runs `bin --json version` for the deyroute binary of a
// release at bin (an absolute, clean path whose base name is deyroute) in
// bin's directory, with no input, only PATH and LC_ALL=C in its
// environment and at most 15 s, and returns what it printed on stdout (at
// most MaxReleaseVersionOutput bytes). The caller has checked the binary
// against the release signature; it runs with the caller's privileges.
// Errors: DEY-X004 for any other path, DEY-X030 when it does not exist,
// DEY-X031 when it does not finish in time, DEY-X007 when it fails or
// prints too much.
func ReleaseVersion(ctx context.Context, bin string) ([]byte, error) {
	desc := Describe(bin, releaseVersionArgs)
	params := deyerr.Params{"command": desc}
	if !filepath.IsAbs(bin) || filepath.Clean(bin) != bin || filepath.Base(bin) != ReleaseBinaryName {
		return nil, deyerr.New(deyerr.X004, params)
	}
	ctx, cancel := context.WithTimeout(ctx, releaseVersionTimeout)
	defer cancel()
	cmd := osexec.CommandContext(ctx, bin, releaseVersionArgs...) // #nosec G204 -- fixed arguments; the caller verified the binary's signature
	cmd.Dir = filepath.Dir(bin)
	cmd.Env = releaseVersionEnv
	stdout := &strictBuffer{limit: MaxReleaseVersionOutput}
	stderr := &cappedBuffer{limit: maxStderrInError}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	switch {
	case ctx.Err() != nil:
		return nil, deyerr.Wrap(deyerr.X031, ctx.Err(), params)
	case stdout.over:
		return nil, deyerr.Wrap(deyerr.X007, errTooMuchOutput, params)
	case err == nil:
		return stdout.buf.Bytes(), nil
	}
	var ee *osexec.ExitError
	if stderrors.As(err, &ee) {
		xe := &ExitError{Command: desc, Code: ee.ExitCode(), Stderr: trimStderr(stderr.Bytes())}
		return nil, deyerr.Wrap(deyerr.X007, xe, params).WithDetail(xe.Stderr)
	}
	if stderrors.Is(err, fs.ErrNotExist) {
		return nil, notFound(bin, err)
	}
	return nil, deyerr.Wrap(deyerr.X007, err, params)
}

// strictBuffer keeps the first limit bytes written to it; a write beyond
// them sets over and fails, which stops the copy.
type strictBuffer struct {
	buf   bytes.Buffer
	limit int
	over  bool
}

func (s *strictBuffer) Write(p []byte) (int, error) {
	room := s.limit - s.buf.Len()
	if len(p) <= room {
		return s.buf.Write(p)
	}
	s.over = true
	n, _ := s.buf.Write(p[:max(room, 0)])
	return n, errTooMuchOutput
}
