# Changelog

All notable changes to DEYROUTE are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added — documentation, integration scenarios, backend hashes

- User guides in Persian and English (`docs/fa/`, `docs/en/`): install, join,
  first tunnel, backup node, troubleshooting with error codes, filtering FAQ,
  hub move, backup/restore, update/uninstall, security; `docs/en/benchmarks.md`
  (method; results not measured yet).
- Integration scenarios S01–S30 of spec section 17 (`test/integration/`):
  hub, nodes and client in systemd containers with real units and nftables;
  `run.sh` builds the release files and reports PASS/FAIL/SKIP per scenario.
  CI runs them for `main` and tags, and on any branch by hand.
- `scripts/manifest-hashes`: fills and re-checks the sha256 of every backend
  in `backends.yaml`. All hashes are now recorded (previously empty, so every
  download-based backend was refused with DEY-S006).
- `scripts/build-backends.sh` and the `backend-builds` workflow: reproducible
  builds of amneziawg-go v1.0.4 and chisel v1.12.1, published on the release
  `backend-builds` (QUESTIONS.md C.29).

### Fixed

- chisel pointed at v1.12.1 release assets that upstream never published
  (HTTP 404); awg/userspace pointed at an unset mirror.

### Added — phase 0 (skeleton)

- Repository layout of section 15, Go module with pinned dependencies.
- `deyroute version` (version, commit, build date, Go version).
- DEYROUTE banner (Unicode and ASCII `#` variants) and the fixed main menu of
  section 6; every item answers "not implemented yet".
- DEY error system with the full code catalog (`internal/errors/codes.go`),
  generated `docs/ERRORS.md`, and a test that rejects codes without Why/Fix.
- All UI strings in `internal/i18n/en.go`.
- Makefile, `.goreleaser.yaml`, `.golangci.yml`, GitHub Actions CI
  (lint → unit → build → smoke on Tier 1 images → integration → release).
- `QUESTIONS.md` with open owner questions and implementation defaults.
