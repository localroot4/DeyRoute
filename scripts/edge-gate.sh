#!/usr/bin/env bash
# Whether .github/workflows/release-edge.yml publishes the commit a green ci
# run tested:
#
#   scripts/edge-gate.sh BRANCH SHA     prints publish=true or publish=false
#                                       (a $GITHUB_OUTPUT line); the reason
#                                       goes to stderr as a notice
#
# The ci runs of a branch end in any order (they run in parallel, and an old
# run can be re-run), while an edge build is numbered by the run of
# release-edge, not by its commit. So SHA is published only when
#   - it is still the tip of BRANCH on origin: when the branch has moved on,
#     the ci run of the newer commit publishes that one; and
#   - it is not in the newest edge release of BRANCH already: neither the
#     same commit again (a re-run) nor an older one.
# Otherwise a newer build could be followed by an older commit under a higher
# version, which the installer and the automatic update would then install.
# release-edge.yml runs this when the ci run has ended and again when the
# publishing job starts; the publishing jobs of a branch run one at a time.
# Needs gh with GH_TOKEN and a clone of the repository (origin) with history.
set -euo pipefail
shopt -s inherit_errexit

usage="usage: scripts/edge-gate.sh BRANCH SHA"
branch=${1:?$usage}
sha=${2:?$usage}
# shellcheck source=scripts/edge-lib.sh
. "$(dirname "$0")/edge-lib.sh"

skip() {
  echo "::notice::not published: $*" >&2
  echo "publish=false"
  exit 0
}

tip=$(git ls-remote origin "refs/heads/$branch" | cut -f1)
[ -n "$tip" ] || skip "branch $branch no longer exists"
[ "$tip" = "$sha" ] ||
  skip "$branch moved on to ${tip:0:7}; the ci run of that commit publishes it, not ${sha:0:7}"

# A commit with a stable tag vX.Y.Z is released by the release job of ci.yml.
# An edge tag on the same commit would make goreleaser, which takes the
# highest tag of the commit, release the edge version instead.
stable=$(git ls-remote --tags origin | awk -v s="$sha" '$1 == s {
  t = $2; sub("^refs/tags/", "", t); sub("\\^\\{\\}$", "", t)
  if (t ~ /^v[0-9]+\.[0-9]+\.[0-9]+$/) { print t; exit }
}')
[ -z "$stable" ] || skip "${sha:0:7} is the stable release $stable, which ci.yml publishes"

# The newest edge release of the branch (releases of the other branch and
# tags without "-edge." do not count).
newest=""
list=$(edge_releases)
while IFS=$'\t' read -r _ tag _ pre; do
  case "$tag" in
    *-edge.*) ;;
    *) continue ;;
  esac
  info=$(edge_branch "$tag" "$pre")
  if [ "${info%%$'\t'*}" = "$branch" ]; then
    newest=$tag
    break
  fi
done <<<"$list"

if [ -n "$newest" ]; then
  # Its tag may be newer than the checkout (published while this run waited).
  git fetch -q --no-tags origin "+refs/tags/$newest:refs/tags/$newest"
  rc=0
  git merge-base --is-ancestor "$sha" "refs/tags/$newest^{commit}" || rc=$?
  case $rc in
    0) skip "${sha:0:7} is already in $newest, the newest edge release of $branch" ;;
    1) ;;
    *)
      echo "::error::cannot compare ${sha:0:7} with $newest" >&2
      exit 1
      ;;
  esac
fi
echo "::notice::publishing ${sha:0:7}, the tip of $branch${newest:+, newer than $newest}" >&2
echo "publish=true"
