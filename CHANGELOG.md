# Changelog

All notable changes to DEYROUTE are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

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
