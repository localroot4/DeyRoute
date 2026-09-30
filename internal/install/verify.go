package install

import (
	"bufio"
	"bytes"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"aead.dev/minisign"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// VerifyMinisign verifies a minisign signature (both the legacy "Ed" and the
// prehashed "ED" BLAKE2b-512 forms, plus the trusted-comment signature) of msg
// against pubKey (the base64 key line, optionally preceded by its "untrusted
// comment:" line). Any failure is DEY-S001 for file "signature".
func VerifyMinisign(msg, sig []byte, pubKey string) error {
	return VerifyMinisignAs(deyerr.S001, "signature", msg, sig, pubKey)
}

// VerifyMinisignAs is VerifyMinisign returning code (DEY-S001 for manifests
// and self-update, DEY-I006 for installer paths) with {file} = file.
func VerifyMinisignAs(code deyerr.Code, file string, msg, sig []byte, pubKey string) error {
	params := deyerr.Params{"file": file}
	var pk minisign.PublicKey
	if err := pk.UnmarshalText([]byte(strings.TrimSpace(pubKey))); err != nil {
		return deyerr.Wrap(code, err, params).WithWhy("the built-in release public key is malformed")
	}
	var s minisign.Signature
	if err := s.UnmarshalText(sig); err != nil {
		return deyerr.Wrap(code, err, params).WithWhy("the signature file is malformed")
	}
	if s.KeyID != pk.ID() {
		return deyerr.New(code, params).WithWhy(fmt.Sprintf(
			"the file was signed with key %X, not with the DEYROUTE release key %X", s.KeyID, pk.ID()))
	}
	// minisign.Verify handles both algorithms (it hashes msg with BLAKE2b-512
	// for prehashed signatures) and checks the trusted comment.
	if !minisign.Verify(pk, msg, sig) {
		return deyerr.New(code, params)
	}
	return nil
}

var sumLineRe = regexp.MustCompile(`^([0-9a-fA-F]{64}) [ *](.+)$`)

// ParseSHA256SUMS parses sha256sum/goreleaser output ("<hex>  <name>" or
// "<hex> *<name>") into name → lowercase hex. Leading "./" is dropped from
// names; blank lines and "#" comments are skipped. A malformed line or a name
// listed twice with different sums is DEY-S001.
func ParseSHA256SUMS(data []byte) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	ln := 0
	for sc.Scan() {
		ln++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		m := sumLineRe.FindStringSubmatch(line)
		if m == nil {
			return nil, deyerr.New(deyerr.S001, deyerr.Params{"file": SumsFile}).
				WithWhy(fmt.Sprintf("line %d is not '<sha256>  <file>'", ln))
		}
		name := strings.TrimPrefix(m[2], "./")
		sum := strings.ToLower(m[1])
		if prev, ok := out[name]; ok && prev != sum {
			return nil, deyerr.New(deyerr.S001, deyerr.Params{"file": SumsFile}).
				WithWhy(fmt.Sprintf("%s is listed twice with different checksums", name))
		}
		out[name] = sum
	}
	if err := sc.Err(); err != nil {
		return nil, deyerr.Wrap(deyerr.S001, err, deyerr.Params{"file": SumsFile})
	}
	if len(out) == 0 {
		return nil, deyerr.New(deyerr.S001, deyerr.Params{"file": SumsFile}).WithWhy("the file lists no checksums")
	}
	return out, nil
}

var archiveRe = regexp.MustCompile(`^deyroute_([0-9A-Za-z.+-]+)_linux_([a-z0-9]+)\.tar\.gz$`)

// ReleaseFromSums finds the release archive for arch in a parsed SHA256SUMS
// and returns its version (no "v"), file name and sha256. With want != "" the
// archive of exactly that version is required. When several versions are
// listed (a mirror directory), the newest is chosen. Missing → DEY-I004.
func ReleaseFromSums(sums map[string]string, arch, want string) (ver, file, sum string, err error) {
	want = strings.TrimPrefix(strings.TrimSpace(want), "v")
	if want == "latest" {
		want = ""
	}
	var cands []string
	for name := range sums {
		m := archiveRe.FindStringSubmatch(path.Base(name))
		if m == nil || m[2] != arch {
			continue
		}
		if want != "" && m[1] != want {
			continue
		}
		cands = append(cands, name)
	}
	if len(cands) == 0 {
		v := want
		if v == "" {
			v = "latest"
		}
		return "", "", "", deyerr.New(deyerr.I004, deyerr.Params{"file": ArchiveName(v, arch)}).
			WithWhy("the release's SHA256SUMS does not list an archive for this version and architecture")
	}
	sort.Slice(cands, func(i, j int) bool {
		vi := archiveRe.FindStringSubmatch(path.Base(cands[i]))[1]
		vj := archiveRe.FindStringSubmatch(path.Base(cands[j]))[1]
		if c, ok := CompareVersions(vi, vj); ok && c != 0 {
			return c > 0
		}
		return cands[i] > cands[j]
	})
	file = cands[0]
	ver = archiveRe.FindStringSubmatch(path.Base(file))[1]
	return ver, file, sums[file], nil
}
