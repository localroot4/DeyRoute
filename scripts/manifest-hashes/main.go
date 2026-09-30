// Command manifest-hashes fills the sha256 fields of the backend manifest
// (internal/backend/backends.yaml) by downloading every pinned asset once, or
// checks that the recorded hashes still match the upstream files.
//
//	go run ./scripts/manifest-hashes            # fill empty hashes
//	go run ./scripts/manifest-hashes -all       # recompute every hash
//	go run ./scripts/manifest-hashes -check     # verify, exit 1 on a mismatch
//	go run ./scripts/manifest-hashes -check -only chisel,wireguard -files dist/backends
//	                                            # verify local builds (scripts/build-backends.sh)
//
// Only the sha256 lines of the file are rewritten, so comments and layout stay
// as they are. URLs that point at the owner's mirror ("{mirror}/…") are
// skipped unless -mirror is given. Downloads honour HTTPS_PROXY.
//
// A hash recorded here is what every installation trusts (spec section 11),
// so compare the output with the checksums upstream publishes where they
// exist (e.g. frp_sha256_checksums.txt, Xray *.dgst, gost checksums.txt)
// before committing.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/backend"
)

// maxAsset bounds one download (the largest pinned asset is ~40 MB).
const maxAsset = 512 << 20

var shaLine = regexp.MustCompile(`(?m)^(\s+sha256:\s*)\{[^}\n]*\}`)

func main() {
	file := flag.String("f", "internal/backend/backends.yaml", "manifest file")
	check := flag.Bool("check", false, "verify the recorded hashes instead of writing")
	all := flag.Bool("all", false, "recompute hashes that are already filled")
	mirror := flag.String("mirror", "", "release base for {mirror} URLs (skipped when empty)")
	only := flag.String("only", "", "comma-separated backend names (default: all)")
	files := flag.String("files", "", "directory with local copies (matched by the URL's file name) used instead of downloading")
	flag.Parse()
	o := options{check: *check, all: *all, mirror: *mirror, files: *files, only: map[string]bool{}}
	for _, n := range strings.Split(*only, ",") {
		if n = strings.TrimSpace(n); n != "" {
			o.only[n] = true
		}
	}
	if err := run(*file, o); err != nil {
		fmt.Fprintln(os.Stderr, "manifest-hashes:", err)
		os.Exit(1)
	}
}

type options struct {
	check, all    bool
	mirror, files string
	only          map[string]bool
}

func run(file string, o options) error {
	check, all, mirror := o.check, o.all, o.mirror
	data, err := os.ReadFile(file) //nolint:gosec // the manifest path the maintainer names
	if err != nil {
		return err
	}
	m, err := backend.ParseManifest(data)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	names := make([]string, 0, len(m.Backends))
	for n := range m.Backends {
		names = append(names, n)
	}
	sort.Strings(names)

	text := string(data)
	var problems []string
	for _, name := range names {
		e := m.Backends[name]
		if e.Builtin || e.System || len(e.URLs) == 0 || len(o.only) > 0 && !o.only[name] {
			continue
		}
		got := map[string]string{}
		for arch, url := range e.SHA256 {
			got[arch] = url
		}
		for _, arch := range sortedKeys(e.URLs) {
			url := e.URLs[arch]
			if strings.Contains(url, "{mirror}") {
				if mirror == "" {
					fmt.Printf("%-10s %-6s skipped (mirror URL)\n", name, arch)
					continue
				}
				url = backend.ResolveURL(url, mirror)
			}
			if !check && !all && e.SHA256[arch] != "" {
				continue
			}
			sum, err := hashOf(client, url, o.files)
			if err != nil {
				return fmt.Errorf("%s/%s: %w", name, arch, err)
			}
			fmt.Printf("%-10s %-6s %s  %s\n", name, arch, sum, url)
			if check && e.SHA256[arch] != sum {
				problems = append(problems, fmt.Sprintf("%s/%s: recorded %q, upstream %s", name, arch, e.SHA256[arch], sum))
			}
			got[arch] = sum
		}
		if !check {
			if text, err = setHashes(text, name, got); err != nil {
				return err
			}
		}
	}
	if check {
		if len(problems) > 0 {
			return errors.New("hash mismatch:\n  " + strings.Join(problems, "\n  "))
		}
		return nil
	}
	if text == string(data) {
		return nil
	}
	if _, err := backend.ParseManifest([]byte(text)); err != nil {
		return fmt.Errorf("rewritten manifest does not parse: %w", err)
	}
	return os.WriteFile(file, []byte(text), 0o644) //nolint:gosec // a source file of the repository
}

// setHashes rewrites the sha256 line inside the block of backend name.
func setHashes(text, name string, sums map[string]string) (string, error) {
	start := strings.Index(text, "\n  "+name+":\n")
	if start < 0 {
		return "", fmt.Errorf("backend %s not found in the manifest text", name)
	}
	end := len(text)
	if next := regexp.MustCompile(`\n  [a-z0-9_-]+:\n`).FindStringIndex(text[start+1:]); next != nil {
		end = start + 1 + next[0]
	}
	block := text[start:end]
	loc := shaLine.FindStringSubmatchIndex(block)
	if loc == nil {
		return "", fmt.Errorf("backend %s has no sha256 line", name)
	}
	parts := make([]string, 0, len(sums))
	for _, arch := range []string{"amd64", "arm64"} {
		parts = append(parts, fmt.Sprintf("%s: %q", arch, sums[arch]))
	}
	line := block[loc[2]:loc[3]] + "{ " + strings.Join(parts, ", ") + " }"
	block = block[:loc[0]] + line + block[loc[1]:]
	return text[:start] + block + text[end:], nil
}

// hashOf hashes the local copy in dir named like the URL's last path element
// when dir is set, else downloads the URL.
func hashOf(c *http.Client, url, dir string) (string, error) {
	if dir == "" {
		return download(c, url)
	}
	f, err := os.Open(filepath.Join(dir, path.Base(url))) //nolint:gosec // a file the maintainer names
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func download(c *http.Client, url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(resp.Body, maxAsset+1))
	if err != nil {
		return "", err
	}
	if n > maxAsset {
		return "", fmt.Errorf("GET %s: larger than %d bytes", url, maxAsset)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sortedKeys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}
