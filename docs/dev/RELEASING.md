# Releasing

Notes for maintainers. Users: see the [README](../../README.md).

## Development builds

Each push to `main` or to the development branch (`dev`) runs
`.github/workflows/ci.yml`: lint, race tests, coverage, govulncheck, build,
smoke and the integration lab on every distribution (the lab runs for `dev`
pushes too). When that run passed, `.github/workflows/release-edge.yml`
builds amd64 and arm64 from **the commit the ci run tested**, signs
`SHA256SUMS` and `backends.yaml` with minisign and publishes
`v<ver>-edge.<run>` with its tag on that commit.

- Nothing is published for a ci run that failed or was cancelled, for a
  `pull_request` run (also one from a fork branch named `main` or `dev`), or
  for a manual ci run. The condition of the `gate` job is: conclusion
  `success`, event `push`, head repository = this repository.
- release-edge starts on `workflow_run` (ci completed). GitHub runs it with
  `release-edge.yml` **as it is on the default branch (`main`)**: a change to
  that file takes effect for both branches once it is merged to `main`. The
  build steps and `scripts/edge-*.sh` come from the tested commit.
- An edge release appears after the whole ci run, about 20 to 40 minutes
  after the push. `<run>` counts every run of release-edge, including the
  ones that publish nothing, so the numbers have gaps.
- The ci runs of a branch end in any order: they run in parallel, and an
  old run can be re-run. `<run>` follows the runs of release-edge, not the
  commits. So a green ci run publishes its commit only if, when the run has
  ended, the commit is still the **tip of its branch** and is **not in the
  newest edge release of that branch** yet (`scripts/edge-gate.sh`, job
  `gate`, which says why it skips). An older commit, a commit whose branch
  has moved on (the ci run of the newer commit publishes that one) and a
  re-run of a commit already published publish nothing. Without this rule
  an older commit could follow a newer one under a higher version, as
  *latest*, and the installer and the automatic update would go back to
  it. After a force-push the new tip is published. When the ci run of the
  tip fails, nothing is published until a later push passes.
- The publishing jobs of a branch run one at a time and never cancel each
  other: a build waits until the one before it is done and then checks the
  rule again (GitHub keeps one waiting build per branch; a newer one takes
  its place). `main` and `dev` publish independently. The retention runs
  after each publish, one at a time.
- A manual run of release-edge (Actions → release-edge → Run workflow) never
  publishes: it prints the version a build of the chosen branch would get
  and the retention dry run (below).
- A build of `main` is marked *latest* until the first stable release, so the
  one-line installer URLs point to the newest `main` build.
- A build of `dev` is always a pre-release. Install one with
  `install.sh --version <ver>` (the release notes show the exact line).
- After a stable tag exists, edge builds are numbered after it
  (`v1.0.1-edge.N`) and published as pre-releases that never become *latest*:
  the installer and `deyroute update` keep resolving to the stable release.
- Retention (`scripts/edge-prune.sh`, run after each publish) deletes old
  edge releases with their tags, but never:
  - a tag without `-edge.` (the stable channel, and `backend-builds`);
  - the release currently marked *latest*;
  - the **10 newest edge builds of each branch** (`main` and `dev` are
    counted separately, so pushes to `dev` never push a `main` build out).

  The same rules hold before and after the first stable tag. The branch of a
  release is the last line of its notes, `branch: <name>`. Releases published
  before that line existed have none: a non-pre-release edge build counts as
  `main` and a pre-release as `dev` (true for every edge build published
  before the first stable tag).
- A version that a hub pins (`join … --version`, `install.sh --version`)
  therefore stays downloadable until 10 newer builds of its branch exist.
  For a fleet pinned for longer, use a stable release.
- To see what the retention would delete, run the workflow by hand
  (Actions → release-edge → Run workflow; a manual run is always a dry
  run): it prints every edge release with its branch, its rank and the
  decision, and publishes and deletes nothing. From a shell with `gh`:
  `bash scripts/edge-prune.sh --dry-run`.

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
