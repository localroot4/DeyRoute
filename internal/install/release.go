package install

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// ReleaseOptions selects a deyroute release to download.
type ReleaseOptions struct {
	// Fetcher downloads (direct, via node or a chain); required.
	Fetcher Fetcher
	// Sources in order (see Sources); required.
	Sources []Source
	// Version to fetch; "" or "latest" = newest.
	Version string
	// Arch is amd64 or arm64.
	Arch string
	// PublicKey verifies SHA256SUMS.minisig; "" = MinisignPublicKey.
	PublicKey string
	// WorkDir receives the archive and the extracted binary (created 0700).
	WorkDir string
	// Retry tunes retries; codes default to S001 (mismatch/signature) and
	// I004 (all sources failed).
	Retry RetryOptions
}

// Release is a downloaded, verified deyroute release.
type Release struct {
	Version string // without "v"
	Archive string // path of the verified archive
	SHA256  string // archive sha256 from the signed SHA256SUMS
	Binary  string // path of the extracted deyroute binary (0755)
	Source  string // name of the source that served SHA256SUMS
}

// DownloadRelease fetches SHA256SUMS and SHA256SUMS.minisig from the first
// source that serves a validly signed pair listing the archive for
// Arch/Version (signature failure = DEY-S001 when no source is valid), downloads
// it from the same source first and the others after (sha256-verified), and
// extracts the deyroute binary. Nothing is installed.
func DownloadRelease(ctx context.Context, opt ReleaseOptions) (*Release, error) {
	if opt.Fetcher == nil || len(opt.Sources) == 0 || opt.WorkDir == "" {
		return nil, deyerr.New(deyerr.I004, deyerr.Params{"file": SumsFile}).
			WithDetail("download not configured (fetcher, sources and work dir are required)")
	}
	pub := opt.PublicKey
	if pub == "" {
		pub = MinisignPublicKey
	}
	if err := os.MkdirAll(opt.WorkDir, 0o700); err != nil {
		return nil, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": opt.WorkDir})
	}
	ro := opt.Retry.withDefaults()
	sigCode := ro.MismatchCode
	var (
		ver, file, sum  string
		srcIdx          = -1
		lastErr, sigErr error
	)
	for i, src := range opt.Sources {
		r := ro
		r.File = SumsFile
		data, _, err := FetchBytes(ctx, opt.Fetcher, []string{src.Sums(opt.Version)}, r)
		if err != nil {
			lastErr = err
			continue
		}
		r.File = SumsSigFile
		sig, _, err := FetchBytes(ctx, opt.Fetcher, []string{src.SumsSig(opt.Version)}, r)
		if err != nil {
			lastErr = err
			continue
		}
		if err := VerifyMinisignAs(sigCode, SumsFile, data, sig, pub); err != nil {
			sigErr = err
			continue
		}
		parsed, err := ParseSHA256SUMS(data)
		if err != nil {
			sigErr = err
			continue
		}
		// A mirror that lags behind (or lacks this arch) is not fatal: the
		// next source may list the archive.
		v, fl, sm, err := ReleaseFromSums(parsed, opt.Arch, opt.Version)
		if err != nil {
			lastErr = err
			continue
		}
		ver, file, sum, srcIdx = v, fl, sm, i
		break
	}
	if srcIdx < 0 {
		return nil, firstErr(sigErr, lastErr, deyerr.New(ro.FailCode, deyerr.Params{"file": SumsFile}))
	}
	// The archive is verified by the signed checksum, so any source may serve
	// it; start with the one that served SHA256SUMS.
	order := append([]Source{opt.Sources[srcIdx]}, opt.Sources[:srcIdx]...)
	order = append(order, opt.Sources[srcIdx+1:]...)
	urls := make([]string, 0, len(order))
	for _, s := range order {
		urls = append(urls, s.URL(ver, file))
	}
	archive := filepath.Join(opt.WorkDir, filepath.Base(file))
	r := opt.Retry
	r.File = filepath.Base(file)
	if err := FetchVerified(ctx, opt.Fetcher, urls, sum, archive, r); err != nil {
		return nil, err
	}
	bins, err := Extract(archive, KindTarGz, []string{BinaryName}, filepath.Join(opt.WorkDir, "bin"))
	if err != nil {
		var de *deyerr.Error
		if stderrors.As(err, &de) && de.Code == deyerr.B001 {
			// The release archive itself is broken: report it as a checksum/
			// content problem of that file, not a backend download.
			return nil, deyerr.Wrap(deyerr.S001, err, deyerr.Params{"file": filepath.Base(file)}).
				WithWhy(de.Detail)
		}
		return nil, err
	}
	return &Release{
		Version: ver,
		Archive: archive,
		SHA256:  sum,
		Binary:  bins[BinaryName],
		Source:  opt.Sources[srcIdx].Name,
	}, nil
}

// FetchManifest downloads backends.yaml and backends.yaml.minisig of version
// ("" = latest) from the first source serving a validly signed pair, and
// parses it strictly. Signature failure → DEY-S001; unreachable → DEY-I004.
// It returns the parsed manifest and the raw bytes for InstallManifest.
func FetchManifest(ctx context.Context, f Fetcher, sources []Source, version, pubKey string, ro RetryOptions) (*backend.ManifestFile, []byte, error) {
	if pubKey == "" {
		pubKey = MinisignPublicKey
	}
	var lastErr, sigErr error
	for _, src := range sources {
		r := ro
		r.File = ManifestFile
		data, _, err := FetchBytes(ctx, f, []string{src.Manifest(version)}, r)
		if err != nil {
			lastErr = err
			continue
		}
		r.File = ManifestSigFile
		sig, _, err := FetchBytes(ctx, f, []string{src.ManifestSig(version)}, r)
		if err != nil {
			lastErr = err
			continue
		}
		if err := VerifyMinisignAs(deyerr.S001, ManifestFile, data, sig, pubKey); err != nil {
			sigErr = err
			continue
		}
		m, err := backend.ParseManifest(data)
		if err != nil {
			sigErr = err
			continue
		}
		return m, data, nil
	}
	return nil, nil, firstErr(sigErr, lastErr,
		deyerr.New(deyerr.I004, deyerr.Params{"file": ManifestFile}).WithDetail("no download source configured"))
}

// firstErr returns the first non-nil error. Verification failures are passed
// first: a source serving badly signed files matters more to the owner than
// another source being unreachable.
func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// InstallManifest writes a verified manifest to Root/etc/deyroute/backends.yaml
// (0644, atomic); backend.LoadManifestOverride activates it.
func InstallManifest(root string, data []byte) error {
	if _, err := backend.ParseManifest(data); err != nil {
		return err
	}
	p := Layout{Root: root}.Path(config.ManifestPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": filepath.Dir(p)})
	}
	return writeFileAtomic(p, data, 0o644)
}
