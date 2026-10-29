#!/usr/bin/env bash
# Version of an edge build, for .github/workflows/release-edge.yml:
#
#   scripts/edge-version.sh BRANCH     prints VERSION=… and LATEST=… ($GITHUB_ENV lines)
#
# Until the first stable tag vX.Y.Z the base is $BASE_VERSION and a build of
# main becomes "latest" (the one-line installer); after it the base is the
# next patch version (v1.0.1-edge.N after v1.0.0) and no edge build becomes
# latest. A build of any other branch (dev) is a pre-release, installed with
# --version. N is $GITHUB_RUN_NUMBER. Needs the tags (fetch-depth: 0).
set -euo pipefail

branch=${1:?usage: scripts/edge-version.sh BRANCH}
stable=$(git tag -l 'v*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n1 || true)
if [ -n "$stable" ]; then
  IFS=. read -r major minor patch <<<"${stable#v}"
  base="${major}.${minor}.$((patch + 1))" latest=false
else
  base="${BASE_VERSION:?}" latest=true
fi
if [ "$branch" != main ]; then latest=false; fi
echo "VERSION=${base}-edge.${GITHUB_RUN_NUMBER:?}"
echo "LATEST=$latest"
