#!/usr/bin/env bash
# Tests of the edge release scripts, with a fake gh and a local git origin (no
# network, no GitHub):
#   - scripts/edge-prune.sh: the retention.
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

# left: the tags of the releases that are left, in version order.
left() { jq -r '.[].tagName' "$FAKE_RELEASES" | sort -V | tr '\n' ' '; }

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
