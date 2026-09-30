package config

import (
	"bytes"
	"os"
	"path/filepath"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"gopkg.in/yaml.v3"
)

// FileMode is the permission of config.yaml (section 2: 0600, owner root).
const FileMode os.FileMode = 0o600

// Marshal encodes c as YAML with a two-space indent, keys in schema order
// (the order of the section 4 sample). It does not validate.
func Marshal(c *Config) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Save validates c (without transport checks, see SaveWith) and writes it to
// path atomically.
func Save(path string, c *Config) error { return SaveWith(path, c, ValidateOptions{}) }

// SaveWith validates c with opts and writes it atomically: a temporary file
// in the same directory (mode 0600) is written and fsynced, renamed over
// path, and the directory is fsynced so the rename survives a crash. A
// reader sees either the old or the new file, never a partial one.
// Validation errors are returned unchanged; I/O failures are DEY-C017.
func SaveWith(path string, c *Config, opts ValidateOptions) error {
	if err := c.Validate(opts); err != nil {
		return err
	}
	data, err := Marshal(c)
	if err != nil {
		return wrapDetail(deyerr.C017, err, deyerr.Params{"path": path})
	}
	if err := WriteFileAtomic(path, data, FileMode); err != nil {
		return wrapDetail(deyerr.C017, err, deyerr.Params{"path": path})
	}
	return nil
}

// WriteFileAtomic writes data to path via a temp file + fsync + rename +
// directory fsync. The temp file is removed on any failure. The file gets
// mode perm even when path already exists with other permissions.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if err = f.Chmod(perm); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(dir)
}

// syncDir fsyncs a directory so a completed rename is durable.
func syncDir(dir string) error {
	d, err := os.Open(dir) // #nosec G304 -- directory of the file being written
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}

// Mutate loads the config at path, applies fn to it, validates with opts and
// saves it atomically, returning the saved config. Nothing is written when
// fn or validation fails. Callers serialize concurrent mutations of the same
// file (the daemon holds its own lock).
func Mutate(path string, opts ValidateOptions, fn func(c *Config) error) (*Config, error) {
	c, err := LoadWith(path, opts)
	if err != nil {
		return nil, err
	}
	if err := fn(c); err != nil {
		return nil, err
	}
	c.ApplyDefaults()
	if err := SaveWith(path, c, opts); err != nil {
		return nil, err
	}
	return c, nil
}
