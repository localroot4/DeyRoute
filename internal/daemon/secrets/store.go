// Package secrets manages the hub's per-tunnel secret material under
// /etc/deyroute/secrets (spec sections 4, 7, 10 and 11):
//
//   - backend-tokens/<tunnel>.token: the shared per-tunnel token (32 random
//     bytes, base64url), identical for every node of the tunnel;
//   - backend-keys/<tunnel>/<backend>.json: key material generated once per
//     (tunnel, backend) by a backend.KeyGenerator;
//   - tls/<tunnel>/: the tunnel TLS certificate in auto mode, the ACME
//     certificate (acme/), the PKCS#12 bundle and its password;
//   - join-tokens.json: single-use join tokens (only their sha256 is stored);
//   - telegram.token: the bot token file;
//   - cloudflare.token: the Cloudflare API token for ACME DNS-01.
//
// Every file is written 0600 (directories 0700) through
// tlsutil.WriteSecret, and every secret value that is loaded or created is
// registered with log.RegisterSecret so it never reaches a log line.
package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	deylog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// Layout of the secrets directory (relative to config.SecretsDir).
const (
	TokensDir     = "backend-tokens"
	KeysDir       = "backend-keys"
	TLSDir        = "tls"
	JoinTokensRel = "join-tokens.json" // #nosec G101 -- file name, not a credential
	TokenSuffix   = ".token"
	// TokenBytes is the size of a tunnel token and of a join token
	// (sections 3, 7 and 11).
	TokenBytes = 32
	// maxSecretFileSize bounds every secret file read from disk.
	maxSecretFileSize = 1 << 20
)

// Store is the per-tunnel secret store of the hub. Root is the filesystem
// root ("/" when empty); CA is the internal CA (required for tls.mode auto);
// Now is the clock (time.Now when nil). A Store is safe for concurrent use.
type Store struct {
	Root string
	CA   *tlsutil.CA
	Now  func() time.Time

	mu  sync.Mutex
	tls map[string]TLSMaterial // last material per tunnel (PKCS12 source)
}

// Dir returns Root/etc/deyroute/secrets.
func (s *Store) Dir() string {
	root := s.Root
	if root == "" {
		root = "/"
	}
	return filepath.Join(root, config.SecretsDir)
}

// JoinTokensPath returns the path of join-tokens.json under Root.
func (s *Store) JoinTokensPath() string { return filepath.Join(s.Dir(), JoinTokensRel) }

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// TokenPath returns the token file of tunnel.
func (s *Store) TokenPath(tunnel string) string {
	return filepath.Join(s.Dir(), TokensDir, tunnel+TokenSuffix)
}

// KeysPath returns the key file of (tunnel, backend).
func (s *Store) KeysPath(tunnel, backendName string) string {
	return filepath.Join(s.Dir(), KeysDir, tunnel, backendName+".json")
}

// TLSPath returns the TLS directory of tunnel.
func (s *Store) TLSPath(tunnel string) string {
	return filepath.Join(s.Dir(), TLSDir, tunnel)
}

// checkID rejects ids that could escape the secrets directory.
func checkID(kind, id string) error {
	if !config.ValidID(id) {
		return deyerr.New(deyerr.C007, deyerr.Params{"kind": kind, "id": id})
	}
	return nil
}

// NewToken returns TokenBytes random bytes encoded base64url without
// padding (43 characters). The value is registered as a secret.
func NewToken() (string, error) {
	b := make([]byte, TokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", deyerr.Wrap(deyerr.X000, err, nil)
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	deylog.RegisterSecret(tok)
	return tok, nil
}

// Token returns the shared token of tunnel, creating it (0600) when it does
// not exist yet. Every node of the tunnel gets the same token (section 7).
func (s *Store) Token(tunnel string) (string, error) {
	if err := checkID("tunnel", tunnel); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.TokenPath(tunnel)
	data, err := readSecret(path)
	switch {
	case err == nil:
		tok := strings.TrimSpace(string(data))
		if tok != "" {
			deylog.RegisterSecret(tok)
			return tok, nil
		}
	case !stderrors.Is(err, fs.ErrNotExist):
		return "", unreadable(path, err)
	}
	return s.writeToken(path)
}

// RotateToken replaces the token of tunnel with a fresh one and returns it
// (deyroute security rotate-tokens, section 11). The caller re-renders and
// restarts the tunnel on the hub and its nodes.
func (s *Store) RotateToken(tunnel string) (string, error) {
	if err := checkID("tunnel", tunnel); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeToken(s.TokenPath(tunnel))
}

func (s *Store) writeToken(path string) (string, error) {
	tok, err := NewToken()
	if err != nil {
		return "", err
	}
	if err := tlsutil.WriteSecret(path, []byte(tok+"\n")); err != nil {
		return "", err
	}
	return tok, nil
}

// BackendKeys returns the key material of (tunnel, backend) persisted in
// backend-keys/<tunnel>/<backend>.json. It is generated with gen the first
// time; later calls keep the stored values. gen may be nil once the file
// exists. When gen returns keys the file does not have yet (a transport of
// the same backend that needs more material), they are added and the file
// is rewritten; stored values always win. Every value is registered as a
// secret. Generation failures are DEY-B009.
func (s *Store) BackendKeys(tunnel, backendName string, gen func() (map[string]string, error)) (map[string]string, error) {
	if err := checkID("tunnel", tunnel); err != nil {
		return nil, err
	}
	if err := checkID("backend", backendName); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.KeysPath(tunnel, backendName)
	stored := map[string]string{}
	data, err := readSecret(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &stored); err != nil {
			return nil, unreadable(path, err)
		}
	case !stderrors.Is(err, fs.ErrNotExist):
		return nil, unreadable(path, err)
	}
	have := len(stored) > 0
	if gen != nil {
		fresh, err := gen()
		if err != nil {
			return nil, deyerr.Wrap(deyerr.B009, err, deyerr.Params{"backend": backendName})
		}
		added := false
		for k, v := range fresh {
			if _, ok := stored[k]; !ok {
				stored[k] = v
				added = true
			}
		}
		if added || !have {
			out, err := json.MarshalIndent(stored, "", "  ")
			if err != nil {
				return nil, deyerr.Wrap(deyerr.X000, err, nil)
			}
			if err := tlsutil.WriteSecret(path, append(out, '\n')); err != nil {
				return nil, err
			}
		}
	} else if !have {
		return nil, unreadable(path, deyerr.Plain("no keys were generated yet"))
	}
	out := make(map[string]string, len(stored))
	for k, v := range stored {
		deylog.RegisterSecret(v)
		out[k] = v
	}
	return out, nil
}

// DeleteTunnel removes every secret of tunnel: its token, its backend keys
// and its TLS directory (section 7.3 step 6). Missing files are not errors.
func (s *Store) DeleteTunnel(tunnel string) error {
	if err := checkID("tunnel", tunnel); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tls, tunnel)
	if err := os.Remove(s.TokenPath(tunnel)); err != nil && !stderrors.Is(err, fs.ErrNotExist) {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": s.TokenPath(tunnel)})
	}
	for _, dir := range []string{filepath.Join(s.Dir(), KeysDir, tunnel), s.TLSPath(tunnel)} {
		if err := os.RemoveAll(dir); err != nil {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
		}
	}
	return nil
}

// Tunnels returns the ids of tunnels that have a token file, sorted (used
// by rotate-tokens for "all tunnels" and by the reconcile loop to find
// secrets of deleted tunnels).
func (s *Store) Tunnels() ([]string, error) {
	dir := filepath.Join(s.Dir(), TokensDir)
	entries, err := os.ReadDir(dir)
	if stderrors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, unreadable(dir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.Type().IsRegular() && strings.HasSuffix(name, TokenSuffix) {
			id := strings.TrimSuffix(name, TokenSuffix)
			if config.ValidID(id) {
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// TelegramToken reads the bot token file (hub.notify.telegram.bot_token_file),
// trims it and registers it as a secret. A missing or empty file is
// DEY-S009.
func TelegramToken(path string) (string, error) {
	data, err := readSecret(path)
	if err != nil {
		return "", unreadable(path, err)
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" {
		return "", unreadable(path, deyerr.Plain("the file is empty"))
	}
	deylog.RegisterSecret(tok)
	return tok, nil
}

// cloudflareTokenRe matches a Cloudflare API token (40 characters today;
// the bounds leave room for other lengths).
var cloudflareTokenRe = regexp.MustCompile(`^[A-Za-z0-9._-]{20,256}$`)

// CloudflareToken reads the Cloudflare API token file
// (hub.acme.cloudflare_token_file, DNS-01 of section 10) like
// TelegramToken. A file that does not hold exactly one token (for example
// "CF_API_TOKEN=…" or two lines) is DEY-S009; the reason never quotes the
// file's content.
func CloudflareToken(path string) (string, error) {
	tok, err := TelegramToken(path)
	if err != nil {
		return "", err
	}
	if !cloudflareTokenRe.MatchString(tok) {
		return "", unreadable(path, deyerr.Plain("the file does not hold one Cloudflare API token (20-256 letters, digits, '.', '_' or '-')"))
	}
	return tok, nil
}

// CheckPerms audits the secrets directory (0700 dirs, 0600 files, root)
// and returns one DEY-S002 per problem (deyroute security audit, doctor).
func (s *Store) CheckPerms() []error { return tlsutil.CheckSecretPerms(s.Dir()) }

// errNotRegular is the reason for a secret path that is a symlink, a
// directory or a special file.
var errNotRegular = deyerr.Plain("not a regular file (symlinks and special files are refused)")

// readSecret reads a bounded secret file. It refuses symlinks and
// non-regular files so a planted link cannot redirect a secret read: the
// file is opened with O_NOFOLLOW (no race between a check and the open)
// and O_NONBLOCK (a planted FIFO cannot block the hub), and the opened
// descriptor must be a regular file.
func readSecret(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) // #nosec G304 -- fixed path under the secrets directory
	if err != nil {
		if stderrors.Is(err, syscall.ELOOP) {
			return nil, errNotRegular
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errNotRegular
	}
	if info.Size() > maxSecretFileSize {
		return nil, deyerr.Plain("file is too large")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSecretFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSecretFileSize {
		return nil, deyerr.Plain("file is too large")
	}
	return data, nil
}

// unreadable wraps a read/parse failure of a secret file as DEY-S009.
func unreadable(path string, err error) error {
	reason := "unreadable"
	if err != nil {
		reason = err.Error()
		if stderrors.Is(err, fs.ErrNotExist) {
			reason = "the file does not exist"
		}
	}
	return deyerr.Wrap(deyerr.S009, err, deyerr.Params{"path": path, "reason": reason})
}
