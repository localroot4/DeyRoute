# Releasing

Notes for maintainers. Users: see the [README](../../README.md).

## Development builds

Each push to `main` or to the development branch (`dev`) runs
`.github/workflows/release-edge.yml`. It tests, builds amd64 and arm64, signs
`SHA256SUMS` and `backends.yaml` with minisign and publishes
`v<ver>-edge.<run>`.

- A build of `main` is marked *latest* until the first stable release, so the
  one-line installer URLs point to the newest `main` build.
- A build of `dev` is always a pre-release. Install one with
  `install.sh --version <ver>` (the release notes show the exact line).
- After a stable tag exists, edge builds are numbered after it
  (`v1.0.1-edge.N`) and published as pre-releases that never become *latest*:
  the installer and `deyroute update` keep resolving to the stable release.
- Only the five newest edge releases are kept; older ones are deleted with
  their tags.

## Stable releases

Push a tag `vX.Y.Z`, for example `git tag v1.0.0 && git push origin v1.0.0`.
CI runs lint, unit tests, the build, the smoke jobs and the integration
scenarios, then goreleaser publishes the signed release. It becomes *latest*
for the installer and for `deyroute update`.

## Signing key

Signing needs the repository secret **`MINISIGN_SECRET_KEY`** (and
`MINISIGN_PASSWORD` if the key is encrypted). The public half is in
`internal/install/keys.go` and `installer/install.sh`. Generate a new pair with
`go run ./scripts/minisign keygen`; if the key changes, update both files
before the next release.

## Backend builds

`.github/workflows/backend-builds.yml` builds the backends that upstream does
not publish as binaries (chisel, amneziawg-go) and republishes the
`backend-builds` release when its inputs change. Run it by hand from the
Actions tab (workflow dispatch) after creating a fresh repository.
