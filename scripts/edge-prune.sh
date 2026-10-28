#!/usr/bin/env bash
# Retention of the edge releases (docs/dev/RELEASING.md), run by
# .github/workflows/release-edge.yml after each publish:
#
#   scripts/edge-prune.sh [--dry-run]
#
# Every release whose tag contains "-edge." is deleted with its tag, except
#   - the release currently marked latest, and
#   - the 10 newest edge builds of each branch (main and dev counted apart).
# A tag without "-edge." (the stable channel) is never touched. The branch of
# a release is the "branch: <name>" line release-edge.yml writes into its
# notes. A release published before that line existed has none; it is from
# main when it is not a pre-release and from dev when it is (until the first
# stable tag only main builds were published as non-pre-releases).
# --dry-run prints the same decisions and deletes nothing. Needs gh with
# GH_TOKEN (its --jq does the JSON work).
set -euo pipefail

KEEP=10
dry=0
case "${1:-}" in
  "") ;;
  --dry-run) dry=1 ;;
  *) echo "usage: scripts/edge-prune.sh [--dry-run]" >&2; exit 2 ;;
esac

# createdAt, tag, isLatest, isPrerelease of every release, newest first.
list=$(gh release list --limit 200 --json tagName,createdAt,isLatest,isPrerelease \
  --jq '.[] | [.createdAt, .tagName, (.isLatest | tostring), (.isPrerelease | tostring)] | @tsv' |
  sort -t "$(printf '\t')" -k1,1r -k2,2Vr)

declare -A rank=()
kept=0 gone=0
while IFS=$'\t' read -r created tag latest pre; do
  [ -n "$tag" ] || continue
  case "$tag" in
    *-edge.*) ;;
    *) printf '%-12s %-26s not an edge build: never deleted\n' keep "$tag"; continue ;;
  esac
  branch=$(gh release view "$tag" --json body --jq .body | tr -d '\r' |
    sed -n 's/^branch: *\([A-Za-z0-9._/-]*\) *$/\1/p' | tail -n 1)
  from="notes"
  if [ -z "$branch" ]; then
    from="legacy rule"
    if [ "$pre" = true ]; then branch=dev; else branch=main; fi
  fi
  n=$((${rank[$branch]:-0} + 1))
  rank[$branch]=$n
  why="#$n of $branch ($from), $created"
  if [ "$latest" = true ] || [ "$n" -le "$KEEP" ]; then
    if [ "$latest" = true ]; then why="$why, latest"; fi
    printf '%-12s %-26s %s\n' keep "$tag" "$why"
    kept=$((kept + 1))
  elif [ "$dry" = 1 ]; then
    printf '%-12s %-26s %s\n' "would delete" "$tag" "$why"
    gone=$((gone + 1))
  else
    gh release delete "$tag" --cleanup-tag --yes
    printf '%-12s %-26s %s\n' deleted "$tag" "$why"
    gone=$((gone + 1))
  fi
done <<<"$list"
if [ "$dry" = 1 ]; then
  echo "edge releases: $kept kept, $gone would be deleted (dry run: nothing deleted)"
else
  echo "edge releases: $kept kept, $gone deleted"
fi
