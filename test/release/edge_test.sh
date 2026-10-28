#!/usr/bin/env bash
# Tests of the edge release scripts, with a fake gh and a local git origin (no
# network, no GitHub):
#   - scripts/edge-gate.sh: whether a green ci run publishes its commit;
#   - scripts/edge-prune.sh: the retention;
#   - the parts of .github/workflows/release-edge.yml that keep the publishes
#     of a branch in order.
# Run by ci.yml (lint job), or by hand from anywhere:
#
#   bash test/release/edge_test.sh [TestName ...]
#
# Needs bash, git and jq.
set -euo pipefail
shopt -s inherit_errexit

root=$(cd "$(dirname "$0")/../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# A clean git: no user configuration (signing, hooks, default branch).
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=edge-test GIT_AUTHOR_EMAIL=edge-test@example.invalid
export GIT_COMMITTER_NAME=edge-test GIT_COMMITTER_EMAIL=edge-test@example.invalid

# The fake gh answers the calls the scripts make (release list, view, delete)
# from $FAKE_RELEASES, a JSON array of {tagName, createdAt, isLatest,
# isPrerelease, body}. A deletion is logged to $FAKE_LOG and removed from the
# array. FAKE_FAIL=<list|view|delete> makes that call fail.
mkdir -p "$tmp/bin"
cat >"$tmp/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[ "${1:-}" = release ] || { echo "fake gh: unsupported: $*" >&2; exit 2; }
sub=$2
shift 2
[ "${FAKE_FAIL:-}" != "$sub" ] || { echo "fake gh: HTTP 502 (release $sub)" >&2; exit 1; }
case $sub in
  list)
    jqexpr=""
    while [ $# -gt 0 ]; do
      case $1 in
        --jq) jqexpr=$2; shift 2 ;;
        --limit | --json) shift 2 ;;
        *) echo "fake gh: release list $1" >&2; exit 2 ;;
      esac
    done
    jq '[.[] | {tagName, createdAt, isLatest, isPrerelease}]' "$FAKE_RELEASES" | jq -r "$jqexpr"
    ;;
  view)
    jq -e --arg t "$1" 'any(.[]; .tagName == $t)' "$FAKE_RELEASES" >/dev/null ||
      { echo "release not found" >&2; exit 1; }
    jq -r --arg t "$1" '.[] | select(.tagName == $t) | .body' "$FAKE_RELEASES"
    ;;
  delete)
    echo "delete $*" >>"$FAKE_LOG"
    jq --arg t "$1" '[.[] | select(.tagName != $t)]' "$FAKE_RELEASES" >"$FAKE_RELEASES.new"
    mv "$FAKE_RELEASES.new" "$FAKE_RELEASES"
    ;;
  *) echo "fake gh: unsupported: release $sub" >&2; exit 2 ;;
esac
EOF
chmod +x "$tmp/bin/gh"
export PATH="$tmp/bin:$PATH"

fail() {
  echo "$*" >&2
  exit 1
}

# new_repo: an empty origin ($T/origin.git) and a working clone ($T/w) on main.
new_repo() {
  git init -q --bare -b main "$T/origin.git"
  git init -q -b main "$T/w"
  git -C "$T/w" remote add origin "$T/origin.git"
}

# on BRANCH: the working clone switches to BRANCH (created at HEAD if new).
on() {
  git -C "$T/w" switch -q "$1" 2>/dev/null || git -C "$T/w" switch -q -c "$1"
}

# commit [-f]: one more commit on the current branch of the working clone,
# pushed to origin (-f: force-pushed); prints its SHA.
commit() {
  echo "$RANDOM" >>"$T/w/f"
  git -C "$T/w" add f
  git -C "$T/w" commit -q -m change
  if [ "${1:-}" = -f ]; then
    git -C "$T/w" push -q -f origin HEAD
  else
    git -C "$T/w" push -q origin HEAD
  fi
  git -C "$T/w" rev-parse HEAD
}

# release TAG SHA BRANCH [PRERELEASE] [LATEST]: a release of SHA, one hour
# after the previous one, with its tag on origin. BRANCH "-" writes no branch
# line (a release published before release-edge.yml wrote one).
n_release=0
release() {
  local tag=$1 sha=$2 branch=$3 pre=${4:-false} latest=${5:-false} body created
  body="Built from \`${sha:0:7}\` (Linux amd64 and arm64, static binary)."
  if [ "$branch" != - ]; then body="$body"$'\n\n'"branch: $branch"; fi
  created=$(printf '2026-10-%02dT%02d:00:00Z' $((1 + n_release / 24)) $((n_release % 24)))
  n_release=$((n_release + 1))
  git --git-dir="$T/origin.git" tag "$tag" "$sha"
  jq --arg t "$tag" --arg c "$created" --argjson p "$pre" --argjson l "$latest" --arg b "$body" \
    '. + [{tagName: $t, createdAt: $c, isLatest: $l, isPrerelease: $p, body: $b}]' \
    "$FAKE_RELEASES" >"$FAKE_RELEASES.new"
  mv "$FAKE_RELEASES.new" "$FAKE_RELEASES"
}

# checkout: the job's checkout, a fresh clone of origin with its tags.
checkout() {
  rm -rf "$T/ci"
  git clone -q "$T/origin.git" "$T/ci"
}

# want_gate WANT BRANCH SHA [REASON]: scripts/edge-gate.sh BRANCH SHA, run in
# the checkout, prints publish=WANT; REASON is part of what it says (stderr).
want_gate() {
  local out
  out=$(cd "$T/ci" && bash "$root/scripts/edge-gate.sh" "$2" "$3" 2>"$T/gate.err") ||
    fail "edge-gate.sh $2 ${3:0:7} failed: $(cat "$T/gate.err")"
  [ "$out" = "publish=$1" ] ||
    fail "edge-gate.sh $2 ${3:0:7}: got '$out', want 'publish=$1' ($(cat "$T/gate.err"))"
  [ -z "${4:-}" ] || grep -qF -- "$4" "$T/gate.err" ||
    fail "edge-gate.sh $2 ${3:0:7}: '$4' missing in: $(cat "$T/gate.err")"
}

# left: the tags of the releases that are left, in version order.
left() { jq -r '.[].tagName' "$FAKE_RELEASES" | sort -V | tr '\n' ' '; }

# ---------------------------------------------------------------- edge-gate

# The ci run of the tip of a branch ends: its commit is published, also when
# older builds of the branch exist.
TestEdgeGatePublishesNewTip() {
  new_repo
  local a b
  a=$(commit)
  b=$(commit)
  release v0.3.0-edge.1 "$a" main false true
  checkout
  want_gate true main "$b" "publishing ${b:0:7}"
}

TestEdgeGatePublishesFirstBuildOfBranch() {
  new_repo
  local a
  a=$(commit)
  checkout
  want_gate true main "$a"
}

# Push A, then B. The ci run of B ends first and B is published; the ci run of
# A ends later (it was slower, or a flaky job of it was re-run). A is behind
# the tip of main: it is not published, so main never goes back to A.
TestEdgeGateSkipsCommitBehindBranchTip() {
  new_repo
  local a b
  a=$(commit)
  b=$(commit)
  release v0.3.0-edge.7 "$b" main false true
  checkout
  want_gate false main "$a" "main moved on to ${b:0:7}"
}

# The same, while the ci run of B is still running: B publishes when it ends.
TestEdgeGateSkipsCommitBehindTipBeforeTipIsPublished() {
  new_repo
  local a b
  a=$(commit)
  b=$(commit)
  checkout
  want_gate false main "$a" "main moved on to ${b:0:7}"
  want_gate true main "$b"
}

# A re-run of the ci run of a commit that is already published (or of the
# publishing workflow) publishes nothing: no second release of one commit.
TestEdgeGateSkipsCommitAlreadyPublished() {
  new_repo
  local a b
  a=$(commit)
  b=$(commit)
  release v0.3.0-edge.1 "$a" main false false
  release v0.3.0-edge.2 "$b" main false true
  checkout
  want_gate false main "$b" "${b:0:7} is already in v0.3.0-edge.2"
}

# The newest release of the branch was published after the job's checkout
# (the job waited for that publish): its tag is fetched for the comparison.
TestEdgeGateFetchesTagPublishedAfterCheckout() {
  new_repo
  local b
  commit >/dev/null
  b=$(commit)
  checkout
  release v0.3.0-edge.2 "$b" main false true
  want_gate false main "$b" "already in v0.3.0-edge.2"
}

# Only builds of the same branch count. dev was published at D; main then
# moves to D (dev merged): main publishes its own build of D. dev publishes
# nothing more for D.
TestEdgeGateCountsOnlyItsBranch() {
  new_repo
  local a d
  a=$(commit)
  on dev
  d=$(commit)
  release v0.3.0-edge.1 "$a" main false true
  release v0.3.0-edge.2 "$d" dev true false
  git -C "$T/w" push -q origin "$d:refs/heads/main"
  checkout
  want_gate true main "$d"
  want_gate false dev "$d" "already in v0.3.0-edge.2"
}

# Releases published before the "branch:" line: a non-pre-release is from
# main, a pre-release from dev.
TestEdgeGateLegacyReleasesByPrerelease() {
  new_repo
  local a d
  a=$(commit)
  on dev
  d=$(commit)
  release v0.3.0-edge.1 "$a" - false true
  release v0.3.0-edge.2 "$d" - true false
  checkout
  want_gate false main "$a" "already in v0.3.0-edge.1"
  want_gate false dev "$d" "already in v0.3.0-edge.2"
}

# After a force-push the published build is not in the branch's history any
# more: the new tip is published.
TestEdgeGatePublishesTipAfterForcePush() {
  new_repo
  local a b c
  a=$(commit)
  b=$(commit)
  release v0.3.0-edge.1 "$b" dev true false
  git -C "$T/w" branch -q -m dev
  git -C "$T/w" push -q origin dev
  git -C "$T/w" reset -q --hard "$a"
  c=$(commit -f)
  checkout
  want_gate true dev "$c"
}

TestEdgeGateSkipsDeletedBranch() {
  new_repo
  local d
  commit >/dev/null
  on dev
  d=$(commit)
  git -C "$T/w" push -q origin --delete dev
  checkout
  want_gate false dev "$d" "dev no longer exists"
}

# A failing gh call fails the gate; it never guesses.
TestEdgeGateFailsWhenGhFails() {
  new_repo
  local a
  a=$(commit)
  release v0.3.0-edge.1 "$a" main false true
  checkout
  local call
  for call in list view; do
    if (cd "$T/ci" && FAKE_FAIL=$call bash "$root/scripts/edge-gate.sh" main "$a") >"$T/out.gate" 2>&1; then
      fail "edge-gate.sh succeeded although gh release $call failed: $(cat "$T/out.gate")"
    fi
    if grep -q '^publish=' "$T/out.gate"; then
      fail "edge-gate.sh decided although gh release $call failed: $(cat "$T/out.gate")"
    fi
  done
}

# --------------------------------------------------------------- edge-prune

# 15 builds of main and 15 of dev after a stable release, plus backend-builds:
# the 10 newest of each branch stay, and every tag without "-edge.".
TestEdgePruneKeepsTenPerBranch() {
  new_repo
  local a n
  a=$(commit)
  release backend-builds "$a" - true false
  release v0.3.0 "$a" - false true
  for n in $(seq 1 30); do
    if [ $((n % 2)) = 1 ]; then release "v0.3.1-edge.$n" "$a" main true; else release "v0.3.1-edge.$n" "$a" dev true; fi
  done
  (cd "$T" && bash "$root/scripts/edge-prune.sh") >"$T/out.prune"
  local want
  want="backend-builds v0.3.0 $(seq -f 'v0.3.1-edge.%g' 11 30 | tr '\n' ' ')"
  [ "$(left)" = "$want" ] || fail "left: $(left); want: $want"
  [ "$(grep -c -- '--cleanup-tag --yes' "$FAKE_LOG")" = 10 ] || fail "gh release delete calls: $(cat "$FAKE_LOG")"
  grep -q 'edge releases: 20 kept, 10 deleted' "$T/out.prune" || fail "summary: $(cat "$T/out.prune")"
}

TestEdgePruneDryRunDeletesNothing() {
  new_repo
  local a n
  a=$(commit)
  for n in $(seq 1 12); do release "v0.3.0-edge.$n" "$a" main false "$([ "$n" = 12 ] && echo true || echo false)"; done
  (cd "$T" && bash "$root/scripts/edge-prune.sh" --dry-run) >"$T/out.prune"
  [ ! -s "$FAKE_LOG" ] || fail "dry run deleted: $(cat "$FAKE_LOG")"
  [ "$(grep -c '^would delete' "$T/out.prune")" = 2 ] || fail "dry run: $(cat "$T/out.prune")"
  grep -q '^would delete *v0.3.0-edge.1 ' "$T/out.prune" || fail "dry run: $(cat "$T/out.prune")"
}

# The release marked latest stays although 10 newer builds of main exist.
TestEdgePruneKeepsLatest() {
  new_repo
  local a n
  a=$(commit)
  for n in $(seq 1 15); do release "v0.3.0-edge.$n" "$a" main false "$([ "$n" = 2 ] && echo true || echo false)"; done
  (cd "$T" && bash "$root/scripts/edge-prune.sh") >"$T/out.prune"
  local want
  want="v0.3.0-edge.2 $(seq -f 'v0.3.0-edge.%g' 6 15 | tr '\n' ' ')"
  [ "$(left)" = "$want" ] || fail "left: $(left); want: $want"
}

# Releases without a "branch:" line: a non-pre-release counts as main, a
# pre-release as dev. 10 main builds (every third) and 20 dev builds.
TestEdgePruneLegacyRule() {
  new_repo
  local a n
  a=$(commit)
  for n in $(seq 1 30); do
    if [ $((n % 3)) = 0 ]; then
      release "v0.3.0-edge.$n" "$a" - false "$([ "$n" = 30 ] && echo true || echo false)"
    else
      release "v0.3.0-edge.$n" "$a" - true
    fi
  done
  (cd "$T" && bash "$root/scripts/edge-prune.sh") >"$T/out.prune"
  local n2 want=""
  for n2 in 3 6 9 12 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do want="${want}v0.3.0-edge.$n2 "; done
  [ "$(left)" = "$want" ] || fail "left: $(left); want: $want"
}

# A failing gh call stops the retention before it deletes anything.
TestEdgePruneStopsWhenGhFails() {
  new_repo
  local a n
  a=$(commit)
  for n in $(seq 1 12); do release "v0.3.0-edge.$n" "$a" main false; done
  if (cd "$T" && FAKE_FAIL=view bash "$root/scripts/edge-prune.sh") >"$T/out.prune" 2>&1; then
    fail "edge-prune.sh succeeded although gh release view failed: $(cat "$T/out.prune")"
  fi
  [ ! -s "$FAKE_LOG" ] || fail "deleted although gh failed: $(cat "$FAKE_LOG")"
}

# ------------------------------------------------------------ release-edge.yml

# The publishes of one branch run one at a time and none cancels another (a
# newer one waits); the gate runs when the ci run ends and again when the
# publishing job starts; the retention runs one at a time, in a job of its own.
TestReleaseEdgeWorkflowPublishesInOrder() {
  local wf="$root/.github/workflows/release-edge.yml"
  # shellcheck disable=SC2016 # a workflow expression, not a shell one
  grep -qF 'group: release-edge-${{ github.event.workflow_run.head_branch }}' "$wf" ||
    fail "release-edge.yml: no concurrency group per branch"
  if grep -qF 'cancel-in-progress: true' "$wf"; then
    fail "release-edge.yml: a run cancels another one in progress"
  fi
  # shellcheck disable=SC2016
  [ "$(grep -cF 'bash scripts/edge-gate.sh "$HEAD_BRANCH" "$HEAD_SHA"' "$wf")" = 2 ] ||
    fail "release-edge.yml: edge-gate.sh must run in the gate job and when the edge job starts"
  grep -qF "if: needs.gate.outputs.publish == 'true'" "$wf" ||
    fail "release-edge.yml: the edge job does not wait for the gate"
  grep -qF 'group: release-edge-retention' "$wf" ||
    fail "release-edge.yml: the retention does not run one at a time"
}

# The signing key is a secret of two environments, not of the repository: an
# older release-edge.yml on an older ref (a tag), which publishes on a manual
# run without the ci gate, then finds no key. Only the jobs that sign name an
# environment; their deployment rules (docs/dev/RELEASING.md) admit main and
# the stable tags.
TestReleaseWorkflowsSignInEnvironments() {
  local wf job env
  for wf in release-edge.yml:edge:edge-release ci.yml:release:release; do
    IFS=: read -r wf job env <<<"$wf"
    wf="$root/.github/workflows/$wf"
    # The environment of each job, as "job environment" lines.
    awk '/^  [a-z-]+:$/ { job = substr($1, 1, length($1) - 1) }
         /^    environment: / { print job, $2 }' "$wf" >"$T/envs"
    [ "$(cat "$T/envs")" = "$job $env" ] ||
      fail "${wf##*/}: want only the job $job in the environment $env, got: $(cat "$T/envs")"
  done
}

# ------------------------------------------------------------------- runner

tests=("$@")
if [ ${#tests[@]} = 0 ]; then
  mapfile -t tests < <(declare -F | awk '$3 ~ /^Test/ { print $3 }')
fi
failed=0
for t in "${tests[@]}"; do
  T="$tmp/$t"
  mkdir -p "$T"
  export FAKE_RELEASES="$T/releases.json" FAKE_LOG="$T/gh.log"
  printf '[]\n' >"$FAKE_RELEASES"
  : >"$FAKE_LOG"
  set +e
  (
    set -e
    "$t"
  ) >"$T/out" 2>&1
  rc=$?
  set -e
  if [ "$rc" = 0 ]; then
    echo "--- PASS: $t"
  else
    echo "--- FAIL: $t"
    sed 's/^/    /' "$T/out"
    failed=$((failed + 1))
  fi
done
if [ "$failed" != 0 ]; then
  echo "FAIL: $failed of ${#tests[@]} tests"
  exit 1
fi
echo "PASS: ${#tests[@]} tests"
