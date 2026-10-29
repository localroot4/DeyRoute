package setup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// The kernel tuning choices a join leaves for the node daemon. Both files
// live in the root-only /var/lib/deyroute and are read without following
// links.
const (
	// HubTuningOffFile marks a node whose join opted out of kernel tuning
	// explicitly (join --sysctl off, or "no" to the kernel question). While
	// it exists the node refuses the hub's sysctl.apply (every profile but
	// off) and tune.plan with DEY-N052. A join that applied a profile, or
	// whose sysctl step failed, never writes it, and nodes joined before it
	// existed do not have it: they follow the hub as before. Lifted with
	// rm /var/lib/deyroute/hub-tuning-off.
	HubTuningOffFile = config.LibDir + "/hub-tuning-off"
	// ReservePortsFile holds the entries of join --reserve-ports, one per
	// line (0600). The node reserves them, with NodeReserved, for every
	// profile it applies except off.
	ReservePortsFile = config.LibDir + "/reserved-ports"
	// MaxReservePorts is the most entries --reserve-ports takes.
	MaxReservePorts = 64
	// maxReservePortsSize bounds the file read (64 entries of "65535-65535").
	maxReservePortsSize = 4096
)

// hostPath is p below root ("/" when empty).
func hostPath(root, p string) string {
	if root == "" {
		root = "/"
	}
	return filepath.Join(root, p)
}

// HubTuningOff reports whether the opt-out marker exists below root as a
// regular file (Lstat: a link is not the marker).
func HubTuningOff(root string) bool {
	fi, err := os.Lstat(hostPath(root, HubTuningOffFile))
	return err == nil && fi.Mode().IsRegular()
}

// WriteHubTuningOff creates the opt-out marker: an empty 0600 regular file,
// opened with O_CREAT|O_EXCL|O_NOFOLLOW so nothing planted at the path is
// followed. An existing regular file is kept. Errors: DEY-X032.
func WriteHubTuningOff(root string) error {
	p := hostPath(root, HubTuningOffFile)
	wrap := func(err error) error { return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p}) }
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // G301: /var/lib/deyroute stays traversable
		return wrap(err)
	}
	f, err := os.OpenFile(filepath.Clean(p), os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0o600)
	if errors.Is(err, fs.ErrExist) && HubTuningOff(root) {
		return nil
	}
	if err != nil {
		return wrap(err)
	}
	return f.Close()
}

// removeHubTuningOff removes a marker left by an earlier join attempt.
func removeHubTuningOff(root string) error {
	p := hostPath(root, HubTuningOffFile)
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
	}
	return nil
}

// CanonicalPortEntry checks one reserved-ports entry: a port 1-65535 or a
// range a-b with a <= b, spaces allowed around the parts. It returns the
// entry without spaces and leading zeros.
func CanonicalPortEntry(s string) (string, bool) {
	port := func(v string) (int, bool) {
		v = strings.TrimSpace(v)
		if v == "" || strings.Trim(v, "0123456789") != "" {
			return 0, false
		}
		n, err := strconv.Atoi(v)
		return n, err == nil && n >= 1 && n <= 65535
	}
	a, b, isRange := strings.Cut(s, "-")
	lo, ok := port(a)
	if !ok {
		return "", false
	}
	if !isRange {
		return strconv.Itoa(lo), true
	}
	hi, ok := port(b)
	if !ok || hi < lo {
		return "", false
	}
	return strconv.Itoa(lo) + "-" + strconv.Itoa(hi), true
}

// SaveReservePorts writes the entries to ReservePortsFile (0600, a new file
// renamed into place, so a link at the path is replaced, never followed);
// no entries removes the file. Errors: DEY-X032.
func SaveReservePorts(root string, entries []string) error {
	p := hostPath(root, ReservePortsFile)
	wrap := func(err error) error { return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p}) }
	if len(entries) == 0 {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return wrap(err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // G301: /var/lib/deyroute stays traversable
		return wrap(err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".reserved-ports.tmp-*") // O_EXCL, 0600
	if err != nil {
		return wrap(err)
	}
	name := tmp.Name()
	_, err = tmp.WriteString(strings.Join(entries, "\n") + "\n")
	if serr := tmp.Sync(); err == nil {
		err = serr
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(name, p)
	}
	if err != nil {
		_ = os.Remove(name)
		return wrap(err)
	}
	return nil
}

// SavedReservePorts reads ReservePortsFile without following links: nil
// when it does not exist; an error (DEY-X000) when it is not a regular file,
// too large or holds an invalid entry.
func SavedReservePorts(root string) ([]string, error) {
	p := hostPath(root, ReservePortsFile)
	bad := func(err error) error { return deyerr.Wrap(deyerr.X000, err, nil).WithDetail(p) }
	f, err := os.OpenFile(filepath.Clean(p), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, bad(err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, bad(err)
	}
	if !fi.Mode().IsRegular() {
		return nil, bad(fmt.Errorf("%s is not a regular file", p))
	}
	data, err := io.ReadAll(io.LimitReader(f, maxReservePortsSize+1))
	if err != nil {
		return nil, bad(err)
	}
	if len(data) > maxReservePortsSize {
		return nil, bad(fmt.Errorf("%s is larger than %d bytes", p, maxReservePortsSize))
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		e, ok := CanonicalPortEntry(line)
		if !ok {
			return nil, bad(fmt.Errorf("%s: invalid entry %q", p, line))
		}
		out = append(out, e)
	}
	return out, nil
}

// NodeTuneReserved are the port entries a node reserves for a profile it
// applies (every profile but off): hub (the entries the hub sent), the
// control range (NodeReserved) and the saved --reserve-ports entries,
// without exact duplicates. When the saved list cannot be read it is left
// out and the error is returned with the rest.
func NodeTuneReserved(root string, hub []string) ([]string, error) {
	saved, err := SavedReservePorts(root)
	out := appendUnique(nil, hub...)
	out = appendUnique(out, NodeReserved()...)
	return appendUnique(out, saved...), err
}

// appendUnique appends the entries not in list yet.
func appendUnique(list []string, entries ...string) []string {
	for _, e := range entries {
		if !slices.Contains(list, e) {
			list = append(list, e)
		}
	}
	return list
}
