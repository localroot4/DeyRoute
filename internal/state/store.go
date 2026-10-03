package state

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	bolt "go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// DefaultPath is the state database location (section 2).
const DefaultPath = "/var/lib/deyroute/state.db"

// File layout constants.
const (
	// FileMode is the permission of state.db and of every snapshot.
	FileMode os.FileMode = 0o600
	// DirMode is used when the parent directory has to be created.
	DirMode os.FileMode = 0o750
	// OpenTimeout bounds the wait for the bbolt file lock.
	OpenTimeout = time.Second
	// BackupSuffix is appended to the database path for the hourly snapshot
	// that Open restores from when the main file is corrupt.
	BackupSuffix = ".bak"
	// CorruptInfix names a corrupt file moved aside: <path>.corrupt-<unix>.
	CorruptInfix = ".corrupt-"
	// SizeBudget is the section 12 resource budget for state.db. The ring
	// buffers and the traffic retention keep the file far below it;
	// doctor/security audit compare Size() against it, and the traffic size
	// guard (SizeGuard) compares the live data (LiveSize) against it.
	SizeBudget int64 = 50 << 20
)

// allBuckets are created on every Open.
var allBuckets = []string{
	BucketNodes, BucketTunnels, BucketProbes, BucketEvents,
	BucketMetrics, BucketCtlPorts, BucketNetIdx, BucketMeta, BucketTraffic,
}

// BackupPath returns the snapshot path Open restores from ("<path>.bak").
func BackupPath(path string) string { return path + BackupSuffix }

// Store is the bbolt-backed runtime state database. Every method is safe
// for concurrent use; bbolt serialises writers and lets readers run in
// parallel. Values are JSON documents, except the fixed-size binary records
// of the traffic time series (traffic.go).
type Store struct {
	db        *bolt.DB
	path      string      // absolute
	ident     os.FileInfo // identity of the open database file (Snapshot guard)
	maxEvents int
	maxProbes int
	now       func() time.Time
	recovered *deyerr.Error
}

// config holds the (unexported) knobs tests use to shrink limits.
type config struct {
	maxEvents int
	maxProbes int
	now       func() time.Time
	timeout   time.Duration
}

type option func(*config)

func withMaxEvents(n int) option            { return func(c *config) { c.maxEvents = n } }
func withMaxProbes(n int) option            { return func(c *config) { c.maxProbes = n } }
func withClock(now func() time.Time) option { return func(c *config) { c.now = now } }
func withTimeout(d time.Duration) option    { return func(c *config) { c.timeout = d } }

// Open opens (or creates) the state database at path with mode 0600 and a
// one second lock timeout, and makes sure every bucket exists.
//
// Corruption never makes Open fail on its own: when bbolt reports an invalid
// file, panics on a damaged page, or a full walk of the tree fails, the file
// is moved aside to <path>.corrupt-<unixtime>, <path>.bak is restored when it
// exists and opens cleanly, otherwise an empty database is created. The
// returned Store then reports a DEY-X001 warning through Recovered(), which
// the daemon logs before continuing.
//
// Open returns an error only when the database cannot be used at all:
// DEY-X020 when another process holds the lock, DEY-X021 for environment
// failures (permissions, read-only or full disk), DEY-X001 when even a fresh
// empty database cannot be created after a corruption.
func Open(path string) (*Store, error) { return openWith(path) }

func openWith(path string, opts ...option) (*Store, error) {
	cfg := config{maxEvents: MaxEvents, maxProbes: MaxProbeSamples, now: time.Now, timeout: OpenTimeout}
	for _, o := range opts {
		o(&cfg)
	}
	if path == "" {
		return nil, deyerr.Wrap(deyerr.X021, errEmptyKey, deyerr.Params{"op": "open", "path": path})
	}
	// Absolute, so Path(), the recovery file names and the Snapshot guard
	// do not depend on the working directory.
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, deyerr.Wrap(deyerr.X021, err, deyerr.Params{"op": "open", "path": path})
	}
	path = abs
	if err := os.MkdirAll(filepath.Dir(path), DirMode); err != nil {
		return nil, deyerr.Wrap(deyerr.X021, err, deyerr.Params{"op": "open", "path": path})
	}

	db, err := openDB(path, cfg.timeout)
	var warn *deyerr.Error
	if err != nil {
		if !isCorrupt(path, err) {
			return nil, openError(path, err)
		}
		db, warn, err = recoverCorrupt(path, err, cfg)
		if err != nil {
			return nil, err
		}
	}
	s := &Store{db: db, path: path, maxEvents: cfg.maxEvents, maxProbes: cfg.maxProbes, now: cfg.now, recovered: warn}
	// bbolt only applies the mode on create; tighten a pre-existing file.
	_ = os.Chmod(path, FileMode)
	if s.ident, err = os.Stat(path); err != nil {
		_ = s.db.Close()
		return nil, deyerr.Wrap(deyerr.X021, err, deyerr.Params{"op": "open", "path": path})
	}
	if err := s.db.Update(func(tx *bolt.Tx) error {
		for _, b := range allBuckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(b)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = s.db.Close()
		return nil, deyerr.Wrap(deyerr.X021, err, deyerr.Params{"op": "create buckets", "path": path})
	}
	return s, nil
}

// Recovered returns the DEY-X001 warning produced when Open found a corrupt
// file and recovered from it (Detail says what was done), or nil.
func (s *Store) Recovered() *deyerr.Error { return s.recovered }

// Path returns the absolute database file path.
func (s *Store) Path() string { return s.path }

// Close closes the database. Further calls return DEY-X021.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return s.fail("close", err)
	}
	return nil
}

// Size returns the size of the database in bytes as it counts against the
// section 12 budget of 50 MB (SizeBudget): the larger of the data in use and
// the file size on disk (bbolt grows the file ahead of the data).
func (s *Store) Size() (int64, error) {
	var n int64
	err := s.db.View(func(tx *bolt.Tx) error {
		n = tx.Size()
		return nil
	})
	if err != nil {
		return 0, s.fail("size", err)
	}
	if st, err := os.Stat(s.path); err == nil && st.Size() > n {
		n = st.Size()
	}
	return n, nil
}

// Snapshot writes a consistent copy of the database to dst (mode 0600)
// through a temporary file, fsync and rename, so dst is always either the
// previous or the new complete snapshot. It is used for <path>.bak (hourly)
// and for backups; writers are not blocked while it runs. A dst that names
// the live database under any spelling (relative path, "..", hard link) is
// refused: renaming over it would detach the open database from its path
// and silently lose every later write.
func (s *Store) Snapshot(dst string) error {
	abs, err := filepath.Abs(dst)
	if err != nil {
		return s.fail("snapshot", err)
	}
	dst = abs
	if s.isLiveFile(dst) {
		return s.fail("snapshot", fmt.Errorf("snapshot target %s is the live database", dst))
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return s.fail("snapshot", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(dst)+".tmp-*")
	if err != nil {
		return s.fail("snapshot", err)
	}
	tmpName := tmp.Name()
	cleanup := func(e error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return s.fail("snapshot", e)
	}
	if err := tmp.Chmod(FileMode); err != nil {
		return cleanup(err)
	}
	if err := s.db.View(func(tx *bolt.Tx) error {
		_, werr := tx.WriteTo(tmp)
		return werr
	}); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return s.fail("snapshot", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return s.fail("snapshot", err)
	}
	syncDir(dir)
	return nil
}

// isLiveFile reports whether path is the open database file (same path or
// same inode as the file opened by Open or currently at s.path).
func (s *Store) isLiveFile(path string) bool {
	if path == s.path {
		return true
	}
	st, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if s.ident != nil && os.SameFile(st, s.ident) {
		return true
	}
	live, err := os.Stat(s.path)
	return err == nil && os.SameFile(st, live)
}

// GetMeta decodes meta/<key> into v. It reports false when the key is absent.
func (s *Store) GetMeta(key string, v any) (bool, error) {
	return s.getJSON("get meta", BucketMeta, key, v)
}

// PutMeta stores v as JSON under meta/<key>.
func (s *Store) PutMeta(key string, v any) error {
	return s.putJSON("put meta", BucketMeta, key, v)
}

// DeleteMeta removes meta/<key> (no error when absent).
func (s *Store) DeleteMeta(key string) error {
	return s.deleteKey("delete meta", BucketMeta, key)
}

// ---------------------------------------------------------------- helpers

var (
	errEmptyKey     = stderrors.New("empty key")
	errNoBucket     = stderrors.New("bucket missing")
	errBackupAbsent = stderrors.New("no backup file")
)

// fail wraps err as DEY-X021 unless it already is a DEY error.
func (s *Store) fail(op string, err error) error {
	var de *deyerr.Error
	if stderrors.As(err, &de) {
		return de
	}
	return deyerr.Wrap(deyerr.X021, err, deyerr.Params{"op": op, "path": s.path})
}

func bucket(tx *bolt.Tx, name string) (*bolt.Bucket, error) {
	b := tx.Bucket([]byte(name))
	if b == nil {
		return nil, fmt.Errorf("%w: %s", errNoBucket, name)
	}
	return b, nil
}

func (s *Store) getJSON(op, bucketName, key string, v any) (bool, error) {
	if key == "" {
		return false, s.fail(op, errEmptyKey)
	}
	var found bool
	err := s.db.View(func(tx *bolt.Tx) error {
		b, err := bucket(tx, bucketName)
		if err != nil {
			return err
		}
		data := b.Get([]byte(key))
		if data == nil {
			return nil
		}
		found = true
		return json.Unmarshal(data, v)
	})
	if err != nil {
		return false, s.fail(op, fmt.Errorf("%s/%s: %w", bucketName, key, err))
	}
	return found, nil
}

func (s *Store) putJSON(op, bucketName, key string, v any) error {
	if key == "" {
		return s.fail(op, errEmptyKey)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return s.fail(op, err)
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		b, err := bucket(tx, bucketName)
		if err != nil {
			return err
		}
		return b.Put([]byte(key), data)
	})
	if err != nil {
		return s.fail(op, fmt.Errorf("%s/%s: %w", bucketName, key, err))
	}
	return nil
}

func (s *Store) deleteKey(op, bucketName, key string) error {
	if key == "" {
		return s.fail(op, errEmptyKey)
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := bucket(tx, bucketName)
		if err != nil {
			return err
		}
		return b.Delete([]byte(key))
	})
	if err != nil {
		return s.fail(op, err)
	}
	return nil
}

// ---------------------------------------------------------------- open & recovery

// corruptError marks a failure that means "the file content is damaged".
type corruptError struct{ cause error }

func (e *corruptError) Error() string { return "corrupt state database: " + e.cause.Error() }
func (e *corruptError) Unwrap() error { return e.cause }

// openDB opens path with bbolt and walks the whole tree. bbolt can panic
// (or fault on the mmap) on damaged pages, so this function — and only this
// one — recovers panics and turns them into a corruptError. Faults on the
// mapped file become panics through debug.SetPanicOnFault for the duration
// of the call (it only affects the current goroutine).
func openDB(path string, timeout time.Duration) (db *bolt.DB, err error) {
	var (
		recording atomic.Bool
		opened    []*os.File // files bbolt opened during Open (closed on panic)
	)
	recording.Store(true)
	opts := &bolt.Options{
		Timeout:      timeout,
		FreelistType: bolt.FreelistMapType,
		OpenFile: func(name string, flag int, perm os.FileMode) (*os.File, error) {
			f, ferr := os.OpenFile(filepath.Clean(name), flag, perm) //nolint:gosec // path chosen by the daemon, not by input
			if ferr == nil && recording.Load() {
				opened = append(opened, f)
			}
			return f, ferr
		},
	}
	prev := debug.SetPanicOnFault(true)
	defer func() {
		debug.SetPanicOnFault(prev)
		recording.Store(false)
		if r := recover(); r != nil {
			if db != nil {
				closeQuietly(db)
			}
			// Releasing the descriptor also releases the flock. The mapping
			// of a database that panicked inside bolt.Open cannot be reached
			// and stays until exit; this only happens once per corruption.
			for _, f := range opened {
				_ = f.Close()
			}
			db = nil
			err = &corruptError{cause: fmt.Errorf("bbolt panic: %v", r)}
		}
	}()
	db, err = bolt.Open(path, FileMode, opts)
	if err != nil {
		return nil, err
	}
	if verr := verify(db); verr != nil {
		closeQuietly(db)
		return nil, &corruptError{cause: verr}
	}
	return db, nil
}

func closeQuietly(db *bolt.DB) {
	defer func() { _ = recover() }()
	_ = db.Close()
}

// verify walks every bucket and key so damaged pages surface now (as an
// error or a panic recovered by openDB) instead of at an arbitrary later
// read inside the daemon.
func verify(db *bolt.DB) error {
	return db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(_ []byte, b *bolt.Bucket) error {
			return walk(b, 0)
		})
	})
}

// maxNesting bounds the recursion of walk. Today's layout nests up to two
// levels (probes/<key>, traffic/<series>/<tier>); the generous bound keeps a newer layout readable after a
// rollback while still stopping a cyclic page reference in a damaged file.
const maxNesting = 16

func walk(b *bolt.Bucket, depth int) error {
	if depth > maxNesting {
		return stderrors.New("bucket nesting too deep (cyclic pages?)")
	}
	return b.ForEach(func(k, v []byte) error {
		if v != nil {
			return nil
		}
		nb := b.Bucket(k)
		if nb == nil {
			return fmt.Errorf("nested bucket %q unreadable", k)
		}
		return walk(nb, depth+1)
	})
}

// isCorrupt decides whether an open failure is caused by the file content
// (recoverable by moving it aside) rather than by the environment.
func isCorrupt(path string, err error) bool {
	var ce *corruptError
	if stderrors.As(err, &ce) {
		return true
	}
	if isEnvError(err) {
		return false
	}
	if stderrors.Is(err, berrors.ErrInvalid) || stderrors.Is(err, berrors.ErrChecksum) ||
		stderrors.Is(err, berrors.ErrVersionMismatch) || stderrors.Is(err, berrors.ErrInvalidMapping) {
		return true
	}
	// Any other bbolt failure on an existing, non-empty file is treated as
	// damaged content (e.g. "file size too small", bad mmap size in meta).
	st, serr := os.Stat(path)
	return serr == nil && st.Mode().IsRegular() && st.Size() > 0
}

// isEnvError reports failures caused by the environment (lock held,
// permissions, resources, filesystem) rather than by the file content;
// they must never move the database aside.
func isEnvError(err error) bool {
	for _, target := range []error{
		berrors.ErrTimeout, fs.ErrPermission,
		syscall.EROFS, syscall.ENOSPC, syscall.EDQUOT, syscall.EMFILE, syscall.ENFILE,
		syscall.EISDIR, syscall.ENOTDIR, syscall.ENOMEM, syscall.EAGAIN, syscall.EINTR,
		syscall.ENODEV, syscall.EBUSY,
	} {
		if stderrors.Is(err, target) {
			return true
		}
	}
	return false
}

func openError(path string, err error) error {
	if stderrors.Is(err, berrors.ErrTimeout) {
		return deyerr.Wrap(deyerr.X020, err, deyerr.Params{"path": path})
	}
	return deyerr.Wrap(deyerr.X021, err, deyerr.Params{"op": "open", "path": path})
}

// recoverCorrupt moves the damaged file aside and opens <path>.bak or an
// empty database in its place.
func recoverCorrupt(path string, cause error, cfg config) (*bolt.DB, *deyerr.Error, error) {
	aside := fmt.Sprintf("%s%s%d", path, CorruptInfix, cfg.now().Unix())
	if _, err := os.Stat(aside); err == nil {
		aside = fmt.Sprintf("%s%s%d", path, CorruptInfix, cfg.now().UnixNano())
	}
	if err := os.Rename(path, aside); err != nil {
		return nil, nil, deyerr.Wrap(deyerr.X001, stderrors.Join(cause, err), deyerr.Params{"path": path})
	}
	detail := []string{"damaged file moved to " + aside}
	if removed := pruneCorrupt(path, keepCorrupt); len(removed) > 0 {
		detail = append(detail, "removed older damaged copies: "+strings.Join(removed, ", "))
	}

	db, berr := restoreBackup(path, cfg.timeout)
	if berr == nil {
		detail = append(detail, "restored from "+BackupPath(path))
	} else {
		if !stderrors.Is(berr, errBackupAbsent) {
			detail = append(detail, "backup "+BackupPath(path)+" unusable: "+berr.Error())
		}
		var err error
		db, err = openDB(path, cfg.timeout)
		if err != nil {
			return nil, nil, deyerr.Wrap(deyerr.X001, stderrors.Join(cause, err), deyerr.Params{"path": path})
		}
		detail = append(detail, "started with an empty state database (history lost, config kept)")
	}
	warn := deyerr.Wrap(deyerr.X001, cause, deyerr.Params{"path": path}).WithDetail(strings.Join(detail, "\n"))
	return db, warn, nil
}

// keepCorrupt is how many <path>.corrupt-<unixtime> copies are kept for
// inspection; each can be as large as the database (section 12: 50 MB),
// and a failing disk could otherwise fill /var/lib/deyroute one restart at a
// time.
const keepCorrupt = 3

// pruneCorrupt deletes all but the newest keep moved-aside copies of path
// and returns the removed names (best effort).
func pruneCorrupt(path string, keep int) []string {
	matches, err := filepath.Glob(globEscape(path) + CorruptInfix + "*")
	if err != nil {
		return nil
	}
	type copyInfo struct {
		name string
		at   int64 // unix nanoseconds
	}
	var copies []copyInfo
	for _, m := range matches {
		v, err := strconv.ParseInt(strings.TrimPrefix(m, path+CorruptInfix), 10, 64)
		if err != nil || v <= 0 {
			continue // not ours
		}
		if v < 1e12 { // seconds (the common name); nanoseconds on a collision
			v *= int64(time.Second)
		}
		copies = append(copies, copyInfo{m, v})
	}
	if len(copies) <= keep {
		return nil
	}
	sort.Slice(copies, func(i, j int) bool { return copies[i].at > copies[j].at })
	var removed []string
	for _, c := range copies[keep:] {
		if os.Remove(c.name) == nil {
			removed = append(removed, filepath.Base(c.name))
		}
	}
	return removed
}

// globEscape quotes the glob metacharacters of a literal path.
func globEscape(p string) string {
	var b strings.Builder
	for _, r := range p {
		if strings.ContainsRune(`*?[\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// restoreBackup copies <path>.bak to path (temp file + rename) and opens it.
// The .bak file itself is never modified.
func restoreBackup(path string, timeout time.Duration) (*bolt.DB, error) {
	bak := BackupPath(path)
	src, err := os.Open(filepath.Clean(bak))
	if err != nil {
		if stderrors.Is(err, fs.ErrNotExist) {
			return nil, errBackupAbsent
		}
		return nil, err
	}
	defer func() { _ = src.Close() }()

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".restore-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	_, cerr := io.Copy(tmp, src)
	if cerr == nil {
		cerr = tmp.Chmod(FileMode)
	}
	if cerr == nil {
		cerr = tmp.Sync()
	}
	if err := tmp.Close(); cerr == nil {
		cerr = err
	}
	if cerr == nil {
		cerr = os.Rename(tmpName, path)
	}
	if cerr != nil {
		_ = os.Remove(tmpName)
		return nil, cerr
	}
	syncDir(dir)
	db, err := openDB(path, timeout)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return db, nil
}

// syncDir fsyncs a directory so a rename is durable (best effort).
func syncDir(dir string) {
	d, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
