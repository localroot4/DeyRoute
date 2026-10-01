package doctor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
)

// BundlePrefix and BundleTimeLayout form the bundle name
// deyroute-doctor-<UTC 20060102T150405Z>.tar.gz (section 13).
const (
	BundlePrefix     = "deyroute-doctor-"
	BundleTimeLayout = "20060102T150405Z"
	BundleExt        = ".tar.gz"
	// DefaultBundleDir is where `deyroute doctor` writes the file (the CLI,
	// running as root, writes it; QUESTIONS.md C.12).
	DefaultBundleDir = "/root"
)

// maxMemberBytes bounds one bundle member (a log tail is ≤ 1 MiB); a
// larger part keeps its last maxMemberBytes (see capMember). A variable
// so tests can lower it.
var maxMemberBytes = 16 << 20

// truncNote starts a member cut by capMember.
const truncNote = "[deyroute doctor: truncated, the first %d bytes were omitted]\n"

// BundleName returns deyroute-doctor-<UTC>.tar.gz for now.
func BundleName(now time.Time) string {
	return BundlePrefix + now.UTC().Format(BundleTimeLayout) + BundleExt
}

// bundleDir is the top directory inside the archive (the name without
// .tar.gz), so extracting it never scatters files.
func bundleDir(now time.Time) string {
	return strings.TrimSuffix(BundleName(now), BundleExt)
}

// NodePrefix is the directory of a node's parts inside the bundle
// (`deyroute doctor --node <id>`).
func NodePrefix(id string) string { return "node-" + id + "/" }

// SectionParts turns sections into bundle parts: "os" → "<prefix>os.txt";
// names with an extension (logs/hub.log) are kept as they are. Section
// names come from the daemon and, for --node, from the node, so they are
// made safe here (".." and empty elements, control characters) instead of
// failing the whole bundle; two names that end up equal get a "-2", "-3"…
// suffix (in sorted order, so the result is deterministic).
func SectionParts(sections map[string]string, prefix string) map[string][]byte {
	names := make([]string, 0, len(sections))
	for name := range sections {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make(map[string][]byte, len(sections))
	for _, name := range names {
		file := safeSectionName(name)
		if path.Ext(path.Base(file)) == "" {
			file += ".txt"
		}
		key := prefix + file
		for i := 2; ; i++ {
			if _, taken := out[key]; !taken {
				break
			}
			ext := path.Ext(file)
			key = prefix + strings.TrimSuffix(file, ext) + "-" + strconv.Itoa(i) + ext
		}
		out[key] = []byte(sections[name])
	}
	return out
}

// safeSectionName maps a section name to a relative archive path: slashes
// and backslashes separate elements, empty and "." elements are dropped, ".." and
// control characters become "_".
func safeSectionName(name string) string {
	var elems []string
	for _, e := range strings.Split(strings.ReplaceAll(name, "\\", "/"), "/") {
		switch e {
		case "", ".":
			continue
		case "..":
			e = "_"
		}
		elems = append(elems, strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return '_'
			}
			return r
		}, e))
	}
	if len(elems) == 0 {
		return "unnamed"
	}
	return strings.Join(elems, "/")
}

// FormatFindings renders findings.txt: one block per finding, most severe
// first.
func FormatFindings(findings []api.DoctorFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", i18n.T(i18n.DoctorFindingsHeader, len(findings)))
	if len(findings) == 0 {
		fmt.Fprintf(&b, "%s %s\n", i18n.T(i18n.DoctorSevOK), i18n.T(i18n.DoctorHealthy))
		return b.String()
	}
	for _, f := range SortFindings(findings) {
		fmt.Fprintf(&b, "%s %s %s\n", i18n.T(styleOf(f.Severity).word), cleanText(f.Rule), cleanText(f.Message))
		if fix := cleanText(f.Fix); fix != "" {
			fmt.Fprintf(&b, "    %s\n", i18n.T(i18n.DoctorFix, fix))
		}
	}
	return b.String()
}

// cleanPartName makes a part name a safe relative archive path: slashes
// only, no leading "/", no "." or ".." elements, no control characters.
func cleanPartName(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	var elems []string
	for _, e := range strings.Split(name, "/") {
		switch e {
		case "", ".":
			continue
		case "..":
			return "", false
		}
		if strings.ContainsFunc(e, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return "", false
		}
		elems = append(elems, e)
	}
	if len(elems) == 0 {
		return "", false
	}
	return strings.Join(elems, "/"), true
}

// WriteBundle writes dir/deyroute-doctor-<UTC>.tar.gz (mode 0600) and
// returns its path. Every part (name → content; see SectionParts and
// NodePrefix) passes through log.RedactingWriter, and findings.txt and
// summary.txt (ANSI colors removed) are added. As a final defense the
// produced archive is read back and scanned for PEM private keys,
// registered secrets and every other secret pattern of the central filter;
// if anything remains the file is not written and DEY-X060 is returned.
// A part name that escapes the bundle ("..") or that equals another one
// after cleaning is DEY-X000; write failures are DEY-X032.
func WriteBundle(dir string, now time.Time, parts map[string][]byte, findings []api.DoctorFinding, summary string) (string, error) {
	if dir == "" {
		dir = DefaultBundleDir
	}
	return WriteBundleFile(filepath.Join(dir, BundleName(now)), now, parts, findings, summary)
}

// WriteBundleFile is WriteBundle with an explicit file path (`deyroute doctor
// --out FILE`); the archive's top directory is still
// deyroute-doctor-<UTC>/. It returns final.
func WriteBundleFile(final string, now time.Time, parts map[string][]byte, findings []api.DoctorFinding, summary string) (string, error) {
	now = now.UTC()
	members := map[string][]byte{}
	for name, data := range parts {
		clean, ok := cleanPartName(name)
		if !ok {
			return "", deyerr.Wrap(deyerr.X000, fmt.Errorf("invalid doctor part name %q", name), nil)
		}
		if _, dup := members[clean]; dup {
			// "a//b" and "a/b": one would silently replace the other.
			return "", deyerr.Wrap(deyerr.X000, fmt.Errorf("two doctor parts are named %q", clean), nil)
		}
		red, err := redactBytes(data)
		if err != nil {
			return "", deyerr.Wrap(deyerr.X000, err, nil)
		}
		members[clean] = capMember(red)
	}
	for name, text := range map[string]string{
		FindingsFile: FormatFindings(findings),
		SummaryFile:  StripANSI(summary),
	} {
		red, err := redactBytes([]byte(text))
		if err != nil {
			return "", deyerr.Wrap(deyerr.X000, err, nil)
		}
		members[name] = capMember(red) // the generated files win over parts of the same name
	}

	archive, err := buildArchive(bundleDir(now), now, members)
	if err != nil {
		return "", deyerr.Wrap(deyerr.X000, err, nil)
	}
	if err := verifyArchive(archive); err != nil {
		return "", err
	}
	if err := writeFile0600(final, archive); err != nil {
		return "", err
	}
	return final, nil
}

// redactBytes passes data through log.RedactingWriter.
func redactBytes(data []byte) ([]byte, error) {
	var b bytes.Buffer
	w := dlog.RedactingWriter(&b)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// capMember keeps the last maxMemberBytes of an already redacted member,
// starting at a line boundary (cutting redacted text can only remove
// material, never reveal any), so verifyArchive always scans whole members.
func capMember(data []byte) []byte {
	if len(data) <= maxMemberBytes {
		return data
	}
	cut := len(data) - maxMemberBytes
	if i := bytes.IndexByte(data[cut:], '\n'); i >= 0 {
		cut += i + 1
	} else {
		cut = len(data)
	}
	return append([]byte(fmt.Sprintf(truncNote, cut)), data[cut:]...)
}

// buildArchive returns a gzip-compressed tar with every member under top/,
// sorted by name, mode 0600, owned by root.
func buildArchive(top string, now time.Time, members map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(members))
	for n := range members {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, n := range names {
		data := members[n]
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     top + "/" + n,
			Mode:     0o600,
			Size:     int64(len(data)),
			ModTime:  now,
			Uname:    "root",
			Gname:    "root",
			Format:   tar.FormatPAX,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// verifyArchive reads the archive back and refuses it (DEY-X060) when a
// member name or content still holds secret material.
func verifyArchive(archive []byte) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return deyerr.Wrap(deyerr.X000, err, nil)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return deyerr.Wrap(deyerr.X000, err, nil)
		}
		if what, found := findSecret([]byte(hdr.Name)); found {
			return deyerr.New(deyerr.X060, deyerr.Params{"file": "(file name)", "what": what})
		}
		if hdr.Size > int64(maxMemberBytes+len(truncNote)+32) {
			// Never produced by WriteBundleFile (capMember); refuse
			// rather than leave a part unscanned.
			return deyerr.New(deyerr.X060, deyerr.Params{"file": path.Base(hdr.Name), "what": "an unscanned oversized part"})
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return deyerr.Wrap(deyerr.X000, err, nil)
		}
		if what, found := findSecret(data); found {
			return deyerr.New(deyerr.X060, deyerr.Params{"file": path.Base(hdr.Name), "what": what})
		}
	}
}

// findSecret reports secret material in redacted text: a PEM private-key
// marker, or a line the central filter would still change (a registered
// secret or a token/key/password pattern; log.Redact is idempotent, so a
// correctly redacted line is a fixed point).
func findSecret(data []byte) (what string, found bool) {
	if bytes.Contains(data, []byte("PRIVATE KEY-----")) {
		return "a private key", true
	}
	for len(data) > 0 {
		line := data
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			data = nil
		}
		if s := string(line); dlog.Redact(s) != s {
			return "a token, key or password", true
		}
	}
	return "", false
}

// writeFile0600 writes data atomically (temp file, fsync, rename) with mode
// 0600. Errors are DEY-X032.
func writeFile0600(final string, data []byte) (err error) {
	wrap := func(err error) error { return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": final}) }
	dir := filepath.Dir(final)
	tmp, err := os.CreateTemp(dir, ".deyroute-doctor-*.tmp")
	if err != nil {
		return wrap(err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return wrap(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return wrap(err)
	}
	if err := tmp.Sync(); err != nil {
		return wrap(err)
	}
	if err := tmp.Close(); err != nil {
		return wrap(err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return wrap(err)
	}
	d, derr := os.Open(dir) // #nosec G304 -- directory of the bundle, fsync after rename
	if derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
