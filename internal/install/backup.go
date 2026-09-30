package install

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"filippo.io/age"
	"gopkg.in/yaml.v3"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/version"
)

// Backup file format (spec section 5): a tar.gz holding manifest.json,
// etc/deyroute/** (modes and numeric uid/gid preserved) and optionally
// events.ndjson; encrypted with age (scrypt passphrase) unless NoEncrypt.
const (
	BackupFormat        = "deyroute-backup"
	BackupFormatVersion = 1
	BackupPrefix        = "deyroute-backup-"
	BackupTimeLayout    = "20060102T150405Z"
	// DefaultAutoKeep is how many automatic pre-apply backups are kept.
	DefaultAutoKeep = 20

	manifestEntry = "manifest.json"
	eventsEntry   = "events.ndjson"
	etcEntry      = "etc/deyroute"
	ageMagic      = "age-encryption.org/"

	maxEntryBytes  int64 = 256 << 20
	maxTotalBytes  int64 = 1 << 30
	maxEventsBytes int64 = 64 << 20
	maxMetaBytes   int64 = 1 << 20
)

// scryptWorkFactor overrides age's scrypt work factor (log2 N) when > 0.
// Only tests lower it; production uses age's default (18, about 1 s).
var scryptWorkFactor int

// BackupManifest is manifest.json inside a backup.
type BackupManifest struct {
	Format        string    `json:"format"`
	FormatVersion int       `json:"format_version"`
	Version       string    `json:"version"`
	Created       time.Time `json:"created"`
	Role          string    `json:"role,omitempty"`
	HubName       string    `json:"hub_name,omitempty"`
	NodeID        string    `json:"node_id,omitempty"`
	Files         int       `json:"files"`
	Events        bool      `json:"events"`
	Skipped       []string  `json:"skipped,omitempty"` // non-regular files not archived
}

// BackupOptions configures Backup.
type BackupOptions struct {
	// Root is the filesystem root ("/" in production).
	Root string
	// OutPath defaults to Root/var/lib/deyroute/backups/
	// deyroute-backup-<UTC 20060102T150405Z>.tar.gz.age (.tar.gz with NoEncrypt).
	OutPath string
	// Passphrase encrypts the archive with age; required unless NoEncrypt.
	Passphrase string
	// NoEncrypt writes a plain tar.gz (`deyroute backup --no-encrypt`).
	NoEncrypt bool
	// Events is the NDJSON event export (state.ExportEvents) stored as
	// events.ndjson; nil = none.
	Events io.Reader
	// Now is the creation time; zero = time.Now().
	Now time.Time
	// Version defaults to version.Version; HubName defaults to hub.name
	// read from config.yaml.
	Version string
	HubName string
}

// Backup archives Root/etc/deyroute (plus manifest.json and events) into a new
// file with mode 0600 and returns its path. Missing /etc/deyroute → DEY-C014,
// empty passphrase without NoEncrypt → DEY-S008, write errors → DEY-X032.
func Backup(opts BackupOptions) (string, error) {
	l := Layout{Root: opts.Root}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	if !opts.NoEncrypt && opts.Passphrase == "" {
		return "", deyerr.New(deyerr.S008, nil)
	}
	out := opts.OutPath
	if out == "" {
		name := BackupPrefix + now.Format(BackupTimeLayout) + ".tar.gz"
		if !opts.NoEncrypt {
			name += ".age"
		}
		out = filepath.Join(l.Path(config.BackupDir), name)
	}
	return writeBackup(l, out, opts, now)
}

// AutoBackup writes an unencrypted backup of Root/etc/deyroute into
// Root/var/lib/deyroute/backups/auto/ (taken before every apply, spec section
// 5) and deletes the oldest ones beyond keep (<= 0 → 20).
func AutoBackup(root string, keep int) (string, error) {
	return autoBackup(root, keep, time.Now())
}

func autoBackup(root string, keep int, now time.Time) (string, error) {
	if keep <= 0 {
		keep = DefaultAutoKeep
	}
	l := Layout{Root: root}
	dir := l.Path(config.AutoBackupDir)
	now = now.UTC()
	// Nanoseconds keep names unique and lexically ordered for pruning.
	name := BackupPrefix + now.Format("20060102T150405.000000000Z") + ".tar.gz"
	p, err := writeBackup(l, filepath.Join(dir, name), BackupOptions{Root: root, NoEncrypt: true}, now)
	if err != nil {
		return "", err
	}
	if err := pruneBackups(dir, keep); err != nil {
		return p, err
	}
	return p, nil
}

// AutoBackups lists the automatic backups, oldest first.
func AutoBackups(root string) ([]string, error) {
	dir := Layout{Root: root}.Path(config.AutoBackupDir)
	names, err := backupNames(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join(dir, n)
	}
	return out, nil
}

func backupNames(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	var names []string
	for _, e := range ents {
		n := e.Name()
		if e.Type().IsRegular() && strings.HasPrefix(n, BackupPrefix) &&
			(strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".tar.gz.age")) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

func pruneBackups(dir string, keep int) error {
	names, err := backupNames(dir)
	if err != nil {
		return err
	}
	var errs []error
	for i := 0; i < len(names)-keep; i++ {
		p := filepath.Join(dir, names[i])
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p}))
		}
	}
	return stderrors.Join(errs...)
}

// configIdentity reads role, hub.name and node.id from a config file without
// strict validation (backups must work even with a config that no longer
// validates).
func configIdentity(data []byte) (role, hub, node string) {
	var c struct {
		Role string `yaml:"role"`
		Hub  *struct {
			Name string `yaml:"name"`
		} `yaml:"hub"`
		Node *struct {
			ID string `yaml:"id"`
		} `yaml:"node"`
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return "", "", ""
	}
	role = c.Role
	if c.Hub != nil {
		hub = c.Hub.Name
	}
	if c.Node != nil {
		node = c.Node.ID
	}
	return role, hub, node
}

type backupItem struct {
	rel  string // archive name, e.g. etc/deyroute/config.yaml
	abs  string
	info fs.FileInfo
}

func writeBackup(l Layout, out string, opts BackupOptions, now time.Time) (string, error) {
	etc := l.Path(config.EtcDir)
	fi, err := os.Stat(etc)
	if err != nil || !fi.IsDir() {
		cause := err
		if cause == nil {
			cause = fmt.Errorf("%s is not a directory", etc)
		}
		return "", deyerr.Wrap(deyerr.C014, cause, deyerr.Params{"path": filepath.Join(etc, "config.yaml")})
	}
	man := BackupManifest{
		Format: BackupFormat, FormatVersion: BackupFormatVersion,
		Version: opts.Version, Created: now, HubName: opts.HubName,
	}
	if man.Version == "" {
		man.Version = version.Version
	}
	data, rerr := os.ReadFile(filepath.Join(etc, "config.yaml")) // #nosec G304 -- fixed path under root
	if rerr == nil {
		role, hub, node := configIdentity(data)
		man.Role, man.NodeID = role, node
		if man.HubName == "" {
			man.HubName = hub
		}
	}
	var items []backupItem
	err = filepath.WalkDir(etc, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, err := filepath.Rel(etc, p)
		if err != nil {
			return err
		}
		name := etcEntry
		if rel != "." {
			name = etcEntry + "/" + filepath.ToSlash(rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.IsDir(), info.Mode().IsRegular():
			items = append(items, backupItem{rel: name, abs: p, info: info})
			if info.Mode().IsRegular() {
				man.Files++
			}
		default:
			man.Skipped = append(man.Skipped, name)
		}
		return nil
	})
	if err != nil {
		return "", deyerr.Wrap(deyerr.C014, err, deyerr.Params{"path": etc})
	}
	var events []byte
	if opts.Events != nil {
		events, err = io.ReadAll(io.LimitReader(opts.Events, maxEventsBytes+1))
		if err != nil {
			return "", deyerr.Wrap(deyerr.X000, err, nil)
		}
		if int64(len(events)) > maxEventsBytes {
			return "", deyerr.New(deyerr.X000, nil).WithDetail("event export larger than 64 MiB")
		}
		man.Events = true
	}

	dir := filepath.Dir(out)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(out)+".tmp-*")
	if err != nil {
		return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": out})
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": out})
	}
	if err := streamBackup(tmp, opts, man, items, events); err != nil {
		return "", err
	}
	if err := finishTemp(tmp, out, 0o600); err != nil {
		return "", err
	}
	ok = true
	return out, nil
}

// streamBackup writes [age →] gzip → tar into w.
func streamBackup(w io.Writer, opts BackupOptions, man BackupManifest, items []backupItem, events []byte) error {
	bw := bufio.NewWriterSize(w, 64<<10)
	var sink io.Writer = bw
	var ageW io.WriteCloser
	if !opts.NoEncrypt {
		rcp, err := age.NewScryptRecipient(opts.Passphrase)
		if err != nil {
			return deyerr.Wrap(deyerr.S008, err, nil)
		}
		if scryptWorkFactor > 0 {
			rcp.SetWorkFactor(scryptWorkFactor)
		}
		ageW, err = age.Encrypt(bw, rcp)
		if err != nil {
			return deyerr.Wrap(deyerr.X000, err, nil)
		}
		sink = ageW
	}
	gz := gzip.NewWriter(sink)
	tw := tar.NewWriter(gz)
	wrap := func(err error) error { return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": "backup archive"}) }

	mj, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return deyerr.Wrap(deyerr.X000, err, nil)
	}
	if err := writeTarBytes(tw, manifestEntry, mj, man.Created); err != nil {
		return wrap(err)
	}
	for _, it := range items {
		if err := writeTarItem(tw, it); err != nil {
			return wrap(err)
		}
	}
	if man.Events {
		if err := writeTarBytes(tw, eventsEntry, events, man.Created); err != nil {
			return wrap(err)
		}
	}
	if err := tw.Close(); err != nil {
		return wrap(err)
	}
	if err := gz.Close(); err != nil {
		return wrap(err)
	}
	if ageW != nil {
		if err := ageW.Close(); err != nil {
			return wrap(err)
		}
	}
	if err := bw.Flush(); err != nil {
		return wrap(err)
	}
	return nil
}

func writeTarBytes(tw *tar.Writer, name string, data []byte, mt time.Time) error {
	h := &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(data)), ModTime: mt, Format: tar.FormatPAX}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func writeTarItem(tw *tar.Writer, it backupItem) error {
	h, err := tar.FileInfoHeader(it.info, "")
	if err != nil {
		return err
	}
	h.Name = it.rel
	if it.info.IsDir() {
		h.Name += "/"
	}
	// Numeric ids plus the names FileInfoHeader looked up: Restore maps
	// by name first because ids (e.g. of group deyroute) differ between servers.
	if st, ok := it.info.Sys().(*syscall.Stat_t); ok {
		h.Uid, h.Gid = int(st.Uid), int(st.Gid)
	}
	h.Format = tar.FormatPAX
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	if !it.info.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(it.abs) // #nosec G304 -- walking our own config tree
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	n, err := io.Copy(tw, io.LimitReader(f, h.Size))
	if err != nil {
		return err
	}
	if n != h.Size {
		return fmt.Errorf("%s changed while being archived", it.abs)
	}
	return nil
}

// RestoreOptions configures Restore.
type RestoreOptions struct {
	// Root is the filesystem root ("/" in production).
	Root string
	// Path is the backup file (.tar.gz.age or .tar.gz).
	Path string
	// Passphrase decrypts .age backups.
	Passphrase string
	// Validate checks etc/deyroute/config.yaml of the backup before anything is
	// replaced (typically config.Parse, which also migrates). Its DEY error
	// is returned unchanged; other errors become DEY-S005.
	Validate func(configYAML []byte) error
	// Now names the pre-restore copy; zero = time.Now().
	Now time.Time
}

// RestoreResult reports a finished restore.
type RestoreResult struct {
	Manifest BackupManifest
	// Events is the events.ndjson content for state import (nil if absent).
	Events []byte
	// Config is the restored config.yaml.
	Config []byte
	// PreviousDir is where the replaced /etc/deyroute was moved
	// (Root/etc/deyroute.pre-restore-<ts>), "" when there was none.
	PreviousDir string
}

// Restore decrypts (DEY-S004 on a wrong passphrase or damaged file), checks
// that the file is a deyroute backup (DEY-S005), validates its config.yaml via
// opts.Validate, then replaces Root/etc/deyroute: the backup is extracted to
// Root/etc/deyroute.restore-tmp, the current directory is renamed to
// Root/etc/deyroute.pre-restore-<ts> and the restored one renamed into place.
func Restore(opts RestoreOptions) (*RestoreResult, error) {
	l := Layout{Root: opts.Root}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	file := filepath.Base(opts.Path)
	f, err := os.Open(opts.Path)
	if err != nil {
		return nil, deyerr.Wrap(deyerr.S005, err, deyerr.Params{"file": file})
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReader(f)
	head, _ := br.Peek(len(ageMagic))
	encrypted := string(head) == ageMagic
	var src io.Reader = br
	if encrypted {
		if opts.Passphrase == "" {
			return nil, deyerr.New(deyerr.S004, deyerr.Params{"file": file}).
				WithWhy("the backup is encrypted and no passphrase was given")
		}
		id, err := age.NewScryptIdentity(opts.Passphrase)
		if err != nil {
			return nil, deyerr.Wrap(deyerr.S004, err, deyerr.Params{"file": file})
		}
		r, err := age.Decrypt(br, id)
		if err != nil {
			var nm *age.NoIdentityMatchError
			if stderrors.As(err, &nm) {
				return nil, deyerr.Wrap(deyerr.S004, err, deyerr.Params{"file": file})
			}
			return nil, deyerr.Wrap(deyerr.S005, err, deyerr.Params{"file": file})
		}
		src = r
	} else if len(head) < 2 || head[0] != 0x1f || head[1] != 0x8b {
		return nil, deyerr.New(deyerr.S005, deyerr.Params{"file": file}).
			WithWhy("the file is neither an age-encrypted nor a gzip backup")
	}
	// Read errors inside an encrypted stream mean it was modified or cut.
	readErr := func(err error) error {
		if encrypted {
			return deyerr.Wrap(deyerr.S004, err, deyerr.Params{"file": file})
		}
		return deyerr.Wrap(deyerr.S005, err, deyerr.Params{"file": file})
	}
	gz, err := gzip.NewReader(src)
	if err != nil {
		return nil, readErr(err)
	}
	defer func() { _ = gz.Close() }()

	etc := l.Path(config.EtcDir)
	parent := filepath.Dir(etc)
	err = os.MkdirAll(parent, 0o755) // #nosec G301 -- this is /etc
	if err != nil {
		return nil, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": parent})
	}
	tmpDir := etc + ".restore-tmp"
	if err := os.RemoveAll(tmpDir); err != nil {
		return nil, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": tmpDir})
	}
	if err := os.Mkdir(tmpDir, 0o700); err != nil {
		return nil, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": tmpDir})
	}
	swapped := false
	defer func() {
		if !swapped {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	res := &RestoreResult{}
	haveManifest, err := extractBackup(tar.NewReader(gz), tmpDir, res, readErr, file)
	if err != nil {
		return nil, err
	}
	// Drain the stream so age authenticates the final chunk.
	if _, err := io.Copy(io.Discard, io.LimitReader(gz, maxTotalBytes)); err != nil {
		return nil, readErr(err)
	}
	if !haveManifest || res.Manifest.Format != BackupFormat {
		return nil, deyerr.New(deyerr.S005, deyerr.Params{"file": file}).WithWhy("manifest.json is missing: not a deyroute backup")
	}
	if res.Manifest.FormatVersion < 1 || res.Manifest.FormatVersion > BackupFormatVersion {
		return nil, deyerr.New(deyerr.S005, deyerr.Params{"file": file}).
			WithWhy(fmt.Sprintf("backup format version %d is not supported by this deyroute", res.Manifest.FormatVersion))
	}
	cfg, err := os.ReadFile(filepath.Join(tmpDir, "config.yaml")) // #nosec G304 -- file we just extracted
	if err != nil {
		return nil, deyerr.New(deyerr.S005, deyerr.Params{"file": file}).WithWhy("the backup contains no etc/deyroute/config.yaml")
	}
	res.Config = cfg
	if opts.Validate != nil {
		if err := opts.Validate(cfg); err != nil {
			var de *deyerr.Error
			if stderrors.As(err, &de) {
				return nil, err
			}
			return nil, deyerr.Wrap(deyerr.S005, err, deyerr.Params{"file": file})
		}
	}

	if _, err := os.Lstat(etc); err == nil {
		prev := etc + ".pre-restore-" + now.UTC().Format(BackupTimeLayout)
		for i := 1; ; i++ {
			if _, err := os.Lstat(prev); os.IsNotExist(err) {
				break
			}
			prev = fmt.Sprintf("%s.pre-restore-%s-%d", etc, now.UTC().Format(BackupTimeLayout), i)
		}
		if err := os.Rename(etc, prev); err != nil {
			return nil, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": etc})
		}
		if err := os.Rename(tmpDir, etc); err != nil {
			_ = os.Rename(prev, etc)
			return nil, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": etc})
		}
		res.PreviousDir = prev
	} else {
		if err := os.Rename(tmpDir, etc); err != nil {
			return nil, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": etc})
		}
	}
	swapped = true
	syncDir(parent)
	return res, nil
}

type dirMode struct {
	path     string
	mode     os.FileMode
	uid, gid int
}

// extractBackup unpacks the tar stream: manifest.json and events.ndjson into
// res, etc/deyroute/** under dst. Only regular files and directories with safe
// names are accepted.
func extractBackup(tr *tar.Reader, dst string, res *RestoreResult, readErr func(error) error, file string) (bool, error) {
	bad := func(why string) error {
		return deyerr.New(deyerr.S005, deyerr.Params{"file": file}).WithWhy(why)
	}
	var own *owners
	if os.Geteuid() == 0 {
		own = newOwners()
	}
	var (
		total        int64
		dirs         []dirMode
		haveManifest bool
	)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, readErr(err)
		}
		if !safeEntryName(h.Name) {
			return false, bad(fmt.Sprintf("entry %q escapes the target directory", h.Name))
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if h.Size > maxEntryBytes || total+h.Size > maxTotalBytes {
			return false, bad(fmt.Sprintf("entry %q is too large", h.Name))
		}
		total += h.Size
		switch {
		case name == manifestEntry && h.Typeflag == tar.TypeReg:
			data, err := readEntry(tr, maxMetaBytes)
			if err != nil {
				return false, readErr(err)
			}
			if err := json.Unmarshal(data, &res.Manifest); err != nil {
				return false, bad("manifest.json is not valid JSON")
			}
			haveManifest = true
		case name == eventsEntry && h.Typeflag == tar.TypeReg:
			data, err := readEntry(tr, maxEventsBytes)
			if err != nil {
				return false, readErr(err)
			}
			res.Events = data
		case name == etcEntry || strings.HasPrefix(name, etcEntry+"/"):
			rel := strings.TrimPrefix(strings.TrimPrefix(name, etcEntry), "/")
			target := filepath.Join(dst, filepath.FromSlash(rel))
			mode := h.FileInfo().Mode().Perm()
			switch h.Typeflag {
			case tar.TypeDir:
				if err := os.MkdirAll(target, 0o700); err != nil {
					return false, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": target})
				}
				d := dirMode{path: target, mode: mode, uid: -1, gid: -1}
				if own != nil {
					d.uid, d.gid = own.ids(h)
				}
				dirs = append(dirs, d)
			case tar.TypeReg:
				if err := restoreFile(tr, target, mode, h, own); err != nil {
					if stderrors.Is(err, errDuplicateEntry) {
						return false, bad(fmt.Sprintf("entry %q appears twice", h.Name))
					}
					var de *deyerr.Error
					if stderrors.As(err, &de) {
						return false, err
					}
					return false, readErr(err)
				}
			default:
				return false, bad(fmt.Sprintf("entry %q is not a regular file or directory", h.Name))
			}
		default:
			// Unknown entries from newer formats are ignored.
		}
	}
	// Apply directory modes deepest first so restrictive modes do not block
	// the writes above.
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i].path) > len(dirs[j].path) })
	for _, d := range dirs {
		if err := os.Chmod(d.path, d.mode); err != nil {
			return false, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": d.path})
		}
		if own != nil {
			if err := os.Lchown(d.path, d.uid, d.gid); err != nil {
				return false, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": d.path})
			}
		}
	}
	return haveManifest, nil
}

var errDuplicateEntry = stderrors.New("duplicate archive entry")

func readEntry(r io.Reader, max int64) ([]byte, error) {
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if n > max {
		return nil, fmt.Errorf("entry larger than %d bytes", max)
	}
	return buf.Bytes(), nil
}

// owners resolves archive owners on the restore target: by user/group name
// when the name exists here, else by the numeric id from the backup.
type owners struct {
	users, groups map[string]int
}

func newOwners() *owners { return &owners{users: map[string]int{}, groups: map[string]int{}} }

func (o *owners) ids(h *tar.Header) (uid, gid int) {
	uid, gid = h.Uid, h.Gid
	if h.Uname != "" {
		if v, ok := o.users[h.Uname]; ok {
			uid = v
		} else if u, err := user.Lookup(h.Uname); err == nil {
			if n, err := strconv.Atoi(u.Uid); err == nil {
				o.users[h.Uname], uid = n, n
			}
		}
	}
	if h.Gname != "" {
		if v, ok := o.groups[h.Gname]; ok {
			gid = v
		} else if g, err := user.LookupGroup(h.Gname); err == nil {
			if n, err := strconv.Atoi(g.Gid); err == nil {
				o.groups[h.Gname], gid = n, n
			}
		}
	}
	return uid, gid
}

func restoreFile(r io.Reader, target string, mode os.FileMode, h *tar.Header, own *owners) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": filepath.Dir(target)})
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- inside our restore dir
	if os.IsExist(err) {
		return errDuplicateEntry
	}
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": target})
	}
	n, err := io.Copy(f, io.LimitReader(r, h.Size))
	if err != nil {
		_ = f.Close()
		return err
	}
	if n != h.Size {
		_ = f.Close()
		return io.ErrUnexpectedEOF
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": target})
	}
	if own != nil {
		uid, gid := own.ids(h)
		if err := f.Chown(uid, gid); err != nil {
			_ = f.Close()
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": target})
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": target})
	}
	if err := f.Close(); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": target})
	}
	if !h.ModTime.IsZero() {
		_ = os.Chtimes(target, h.ModTime, h.ModTime)
	}
	return nil
}
