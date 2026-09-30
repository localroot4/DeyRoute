package tlsutil

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Required permissions inside /etc/deyroute/secrets (sections 4 and 11).
const (
	SecretFileMode fs.FileMode = 0o600
	SecretDirMode  fs.FileMode = 0o700
)

// geteuid is replaced in tests to exercise the root-only owner check.
var geteuid = os.Geteuid

// WriteSecret writes data to path atomically with mode 0600: missing parent
// directories are created 0700, the data goes to a temporary file in the
// same directory which is fsynced and renamed over path, then the directory
// is fsynced. The file is owned by the calling user (root for the daemons).
// Failures are DEY-X032.
func WriteSecret(path string, data []byte) error {
	return writeSecretFiles(secretFile{path: path, data: data})
}

// WriteSecretPair writes a certificate and its key like WriteSecret, but
// stages both files (temporary file, fsync) before replacing either, so a
// failure while writing (disk full, read-only mount) leaves the old pair
// untouched instead of a new certificate next to an old key.
func WriteSecretPair(certPath string, certPEM []byte, keyPath string, keyPEM []byte) error {
	return writeSecretFiles(secretFile{path: keyPath, data: keyPEM}, secretFile{path: certPath, data: certPEM})
}

// secretFile is one file of a secret write.
type secretFile struct {
	path string
	data []byte
}

// writeSecretFiles stages every file, then renames them in order and
// fsyncs their directories.
func writeSecretFiles(files ...secretFile) error {
	staged := make([]string, len(files))
	defer func() {
		for _, tmp := range staged {
			if tmp != "" {
				_ = os.Remove(tmp)
			}
		}
	}()
	for i, f := range files {
		tmp, err := stageSecret(f.path, f.data)
		if err != nil {
			return err
		}
		staged[i] = tmp
	}
	var dirs []string
	for i, f := range files {
		if err := os.Rename(staged[i], f.path); err != nil {
			return writeErr(f.path, err)
		}
		staged[i] = ""
		if d := filepath.Dir(f.path); !contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	for _, d := range dirs {
		syncDir(d)
	}
	return nil
}

// stageSecret writes data to a fsynced 0600 temporary file next to path
// (creating missing parents 0700) and returns its name.
func stageSecret(path string, data []byte) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, SecretDirMode); err != nil {
		return "", writeErr(path, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", writeErr(path, err)
	}
	tmpName := tmp.Name()
	fail := func(err error) (string, error) {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", writeErr(path, err)
	}
	if err := tmp.Chmod(SecretFileMode); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", writeErr(path, err)
	}
	return tmpName, nil
}

// syncDir fsyncs a directory so a rename survives a crash. Errors are
// ignored: some filesystems do not support fsync on directories.
func syncDir(dir string) {
	d, err := os.Open(dir) // #nosec G304 -- parent directory of a file deyroute writes
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

func writeErr(path string, err error) error {
	return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path})
}

// CheckSecretPerms walks dir and returns one DEY-S002 error per problem:
// files that are not 0600, directories that are not 0700, symlinks or
// special files, and — only when the process runs as root — anything not
// owned by root (uid 0). A missing dir yields nil (nothing to audit).
func CheckSecretPerms(dir string) []error {
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return nil
	}
	var out []error
	asRoot := geteuid() == 0
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			out = append(out, deyerr.Wrap(deyerr.S002, err, deyerr.Params{"path": path, "mode": "unreadable"}).
				WithWhy("the path cannot be inspected: "+err.Error()))
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			out = append(out, deyerr.Wrap(deyerr.S002, err, deyerr.Params{"path": path, "mode": "unreadable"}).
				WithWhy("the path cannot be inspected: "+err.Error()))
			return nil
		}
		if e := checkOne(path, info, asRoot); e != nil {
			out = append(out, e)
		}
		return nil
	})
	if walkErr != nil {
		out = append(out, deyerr.Wrap(deyerr.S002, walkErr, deyerr.Params{"path": dir, "mode": "unreadable"}))
	}
	return out
}

// unixPerm returns the permission bits of mode including setuid, setgid and
// sticky, in their octal chmod positions.
func unixPerm(mode fs.FileMode) uint32 {
	p := uint32(mode.Perm())
	if mode&fs.ModeSetuid != 0 {
		p |= 0o4000
	}
	if mode&fs.ModeSetgid != 0 {
		p |= 0o2000
	}
	if mode&fs.ModeSticky != 0 {
		p |= 0o1000
	}
	return p
}

func checkOne(path string, info fs.FileInfo, asRoot bool) error {
	mode := info.Mode()
	perm := unixPerm(mode)
	modeStr := fmt.Sprintf("%04o", perm)
	switch {
	case mode&fs.ModeSymlink != 0:
		return deyerr.New(deyerr.S002, deyerr.Params{"path": path, "mode": "symlink"}).
			WithWhy("secrets must be regular files, a symlink can point anywhere").
			WithFix("replace the symlink with the real file: cp --remove-destination \"$(readlink -f " + path + ")\" " + path)
	case mode.IsDir():
		if perm != uint32(SecretDirMode) {
			return deyerr.New(deyerr.S002, deyerr.Params{"path": path, "mode": modeStr}).
				WithWhy("secret directories must be 0700 and owned by root").
				WithFix("chmod 700 " + path + " && chown root:root " + path)
		}
	case mode.IsRegular():
		if perm != uint32(SecretFileMode) {
			return deyerr.New(deyerr.S002, deyerr.Params{"path": path, "mode": modeStr})
		}
	default:
		return deyerr.New(deyerr.S002, deyerr.Params{"path": path, "mode": mode.Type().String()}).
			WithWhy("only regular files and directories belong in the secrets directory").
			WithFix("remove " + path)
	}
	if asRoot {
		if uid, ok := fileUID(info); ok && uid != 0 {
			e := deyerr.New(deyerr.S002, deyerr.Params{"path": path, "mode": modeStr + " uid " + strconv.FormatUint(uint64(uid), 10)}).
				WithWhy("secrets must be owned by root, this one is owned by uid " + strconv.FormatUint(uint64(uid), 10))
			if mode.IsDir() {
				e = e.WithFix("chown root:root " + path + " && chmod 700 " + path)
			}
			return e
		}
	}
	return nil
}
