// Package supervise runs several backend processes as one deyroute-tun@ unit
// ("deyroute pair"): a Backhaul tunnel with TCP and UDP port maps on a
// TCP-only transport runs its UDP maps in a second Backhaul process. The
// processes live and die together: when one exits the others are stopped
// and the unit fails, so systemd restarts the whole set.
package supervise

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Command is the deyroute subcommand that runs a set of processes as one
// unit: "deyroute pair <prog> [args] -- <prog> [args]".
const Command = "pair"

// Separator separates the command lines of "deyroute pair".
const Separator = "--"

// StopTimeout is how long the remaining processes get between SIGTERM and
// SIGKILL.
const StopTimeout = 5 * time.Second

// Split splits the arguments of "deyroute pair" into command lines at every
// Separator. Every command line needs an absolute, clean program path; at
// least two command lines are required.
func Split(args []string) ([][]string, error) {
	var out [][]string
	cur := []string{}
	for _, a := range args {
		if a == Separator {
			out = append(out, cur)
			cur = []string{}
			continue
		}
		cur = append(cur, a)
	}
	out = append(out, cur)
	if len(out) < 2 {
		return nil, errors.New("pair: at least two command lines separated by " + Separator + " are required")
	}
	for i, argv := range out {
		if len(argv) == 0 {
			return nil, fmt.Errorf("pair: command line %d is empty", i+1)
		}
		if p := argv[0]; !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return nil, fmt.Errorf("pair: program %q is not an absolute path", p)
		}
	}
	return out, nil
}

// Run starts every command line with stdout and stderr shared, waits until
// one of them exits or ctx ends, then stops the others (SIGTERM, SIGKILL
// after StopTimeout). It returns nil when ctx ended (the unit is stopping)
// and an error naming the process that exited otherwise, also for a clean
// exit: the set is only useful complete.
func Run(ctx context.Context, cmds [][]string, stdout, stderr io.Writer) error {
	type exit struct {
		i   int
		err error
	}
	procs := make([]*osexec.Cmd, 0, len(cmds))
	done := make(chan exit, len(cmds))
	var result error
	// Files are shared as they are; other writers are copied to by one
	// goroutine per process and need a lock.
	var mu sync.Mutex
	stdout, stderr = locked(stdout, &mu), locked(stderr, &mu)
	for i, argv := range cmds {
		c := osexec.Command(argv[0], argv[1:]...) // #nosec G204 -- the unit's own command lines (validated by Split; nodes check the programs)
		c.Stdout, c.Stderr = stdout, stderr
		if err := c.Start(); err != nil {
			result = fmt.Errorf("pair: start %s: %w", argv[0], err)
			break
		}
		procs = append(procs, c)
		go func(i int, c *osexec.Cmd) { done <- exit{i, c.Wait()} }(i, c)
	}
	remaining := len(procs)
	if result == nil {
		select {
		case <-ctx.Done():
		case first := <-done:
			remaining--
			result = fmt.Errorf("pair: %s exited", cmds[first.i][0])
			if first.err != nil {
				result = fmt.Errorf("pair: %s exited: %w", cmds[first.i][0], first.err)
			}
		}
	}
	for _, c := range procs {
		_ = c.Process.Signal(syscall.SIGTERM)
	}
	timer := time.NewTimer(StopTimeout)
	defer timer.Stop()
	for remaining > 0 {
		select {
		case <-done:
			remaining--
		case <-timer.C:
			for _, c := range procs {
				_ = c.Process.Kill()
			}
			timer.Reset(StopTimeout)
		}
	}
	return result
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func locked(w io.Writer, mu *sync.Mutex) io.Writer {
	if _, ok := w.(*os.File); ok || w == nil {
		return w
	}
	return lockedWriter{mu: mu, w: w}
}
