#!/usr/bin/env bash
# Integration scenarios S01-S30 (spec section 17) and S31-S33 (acceptance
# criteria of section 16 that S01-S30 do not cover) in Docker containers
# running systemd. Usage (from anywhere; needs docker compose, go, jq):
#
#   DISTRO=ubuntu-24.04 test/integration/run.sh            # every scenario
#   SCENARIOS="S01 S07" test/integration/run.sh            # a selection
#
# Environment: DISTRO (ubuntu-22.04|ubuntu-24.04|ubuntu-26.04|debian-12|
# debian-13), DEY_IMAGE (use a prebuilt image instead of building one),
# DEY_OFFLINE=1 (no internet in the containers: only direct/native tunnels;
# scenarios that need other backends are skipped; the client has no Xray),
# DEY_PROXY / DEY_PROXY_CA (the containers reach the internet only through this
# HTTPS proxy; see lib.sh use_proxy), DEY_ARTIFACTS (where results and
# diagnostics go; default dist/integration).
set -euo pipefail

IT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ROOT=$(cd "$IT_DIR/../.." && pwd)
DISTRO=${DISTRO:-ubuntu-24.04}
case $DISTRO in
  ubuntu-22.04 | ubuntu-24.04 | ubuntu-26.04 | debian-12 | debian-13) BASE=${DISTRO/-/:} ;;
  *) echo "unknown DISTRO $DISTRO" >&2; exit 2 ;;
esac
export DEY_IMAGE=${DEY_IMAGE:-deyroute-it:$DISTRO}
export DEY_VERSION=${DEY_VERSION:-0.9.0-it.1}
export DEY_OLD_VERSION=${DEY_OLD_VERSION:-0.0.1-it.1}
ART=${DEY_ARTIFACTS:-$ROOT/dist/integration}
mkdir -p "$ART"
ART=$(cd "$ART" && pwd)

if [ "${DEY_REBUILD:-0}" = 1 ] || ! docker image inspect "$DEY_IMAGE" >/dev/null 2>&1; then
  echo "building $DEY_IMAGE from $BASE" >&2
  docker build --build-arg "BASE=$BASE" -t "$DEY_IMAGE" "$IT_DIR" >&2
fi

# The "release" the installer consumes offline: archives for this version and
# an old one (S28), SHA256SUMS, install.sh, backends.yaml and CHANGELOG.md, as
# published.
export DEY_DIST="$ART/release"
rm -rf "$DEY_DIST"
mkdir -p "$DEY_DIST"
case $(uname -m) in x86_64) GOARCH=amd64 ;; aarch64 | arm64) GOARCH=arm64 ;; *) GOARCH=$(uname -m) ;; esac
for v in "$DEY_VERSION" "$DEY_OLD_VERSION"; do
  d="$DEY_DIST/deyroute_${v}_linux_${GOARCH}"
  mkdir -p "$d"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -trimpath \
    -ldflags "-s -w -X main.version=$v -X main.commit=it" -o "$d/deyroute" ./cmd/deyroute)
  cp "$ROOT/README.md" "$ROOT/CHANGELOG.md" "$d/"
  tar -C "$DEY_DIST" -czf "$d.tar.gz" "${d##*/}"
  rm -rf "$d"
done
cp "$ROOT/installer/install.sh" "$ROOT/internal/backend/backends.yaml" "$ROOT/CHANGELOG.md" "$DEY_DIST/"
(cd "$DEY_DIST" && sha256sum deyroute_*.tar.gz install.sh backends.yaml CHANGELOG.md >SHA256SUMS)

if [ -n "${SCENARIOS:-}" ]; then
  list=()
  for s in $SCENARIOS; do list+=("$IT_DIR/scenarios/$s.sh"); done
else
  list=("$IT_DIR"/scenarios/S*.sh)
fi

results="$ART/results-$DISTRO.txt"
: >"$results"
failed=0
for f in "${list[@]}"; do
  id=$(basename "$f" .sh)
  export SCENARIO=$id DEY_PROJECT="deyit-${id,,}"
  dc() { docker compose -f "$IT_DIR/compose.yml" --profile hub2 "$@"; }
  dc down -v --remove-orphans >/dev/null 2>&1 || true
  start=$SECONDS rc=0
  bash "$f" >"$ART/$id.log" 2>&1 || rc=$?
  dur=$((SECONDS - start))
  case $rc in
    0) st=PASS ;;
    77) st=SKIP ;;
    *) st=FAIL ;;
  esac
  title=$(grep -m1 -E '^# S[0-9]+:' "$f" | sed 's/^# S[0-9]*: *//')
  printf '%-4s %-4s %4ss  %s\n' "$id" "$st" "$dur" "$title" | tee -a "$results"
  if [ "$st" = FAIL ]; then
    failed=$((failed + 1))
    tail -n 25 "$ART/$id.log" | sed 's/^/     | /'
    bash -c 'source "$1/lib.sh" && diagnostics "$2"' _ "$IT_DIR" "$ART/$id-diagnostics" || true
  fi
  [ "${DEY_KEEP:-0}" = 1 ] || dc down -v --remove-orphans >/dev/null 2>&1 || true
done
echo "results: $results  logs: $ART" >&2
[ "$failed" -eq 0 ]
