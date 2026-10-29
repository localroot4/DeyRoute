#!/usr/bin/env bash
# Sourced by the edge release scripts (scripts/edge-*.sh), not run itself:
# the releases of the repository and the branch of an edge build. Needs gh
# with GH_TOKEN (its --jq does the JSON work). A failing gh call makes the
# function fail; the scripts run with set -e and stop.

# edge_releases prints every release, newest first, one per line:
# createdAt TAB tag TAB isLatest TAB isPrerelease.
edge_releases() {
  gh release list --limit 200 --json tagName,createdAt,isLatest,isPrerelease \
    --jq '.[] | [.createdAt, .tagName, (.isLatest | tostring), (.isPrerelease | tostring)] | @tsv' |
    sort -t "$(printf '\t')" -k1,1r -k2,2Vr
}

# edge_branch TAG PRERELEASE prints the branch of an edge release, a TAB and
# where it was read: the last "branch: <name>" line of the notes, which
# release-edge.yml writes. A release published before that line existed has
# none: it is from main when it is not a pre-release and from dev when it is
# (until the first stable tag only main builds were published as
# non-pre-releases).
edge_branch() {
  local branch
  branch=$(gh release view "$1" --json body --jq .body | tr -d '\r' |
    sed -n 's/^branch: *\([A-Za-z0-9._/-]*\) *$/\1/p' | tail -n 1) || return 1
  if [ -n "$branch" ]; then
    printf '%s\tnotes\n' "$branch"
  elif [ "$2" = true ]; then
    printf 'dev\tlegacy rule\n'
  else
    printf 'main\tlegacy rule\n'
  fi
}
