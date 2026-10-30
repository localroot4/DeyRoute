#!/usr/bin/env bash
# Integration scenarios S01-S30 (spec section 17), S31-S33 (acceptance
# criteria of section 16 that S01-S30 do not cover), S34 (front mode),
# S35 (a measurement of "passes, then cut" filtering; its numbers go to
# S35-measure.txt beside the logs), S36 (links a backend user plants in
# the tunnel log directory), S37 (nft flush ruleset on hub and node: table
# inet deyroute comes back by itself), S38 (silent raw-TCP targets behind
# the echo probe), S39 (the primary node replaced by a new one), S41 (a
# hub that serves a tampered deyroute binary: the node refuses it) and S42
# (Hysteria2 port hopping scoped to the hub) in Docker containers running
# systemd.
# Usage (from anywhere; needs docker compose, go, jq):
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
export ART

if [ "${DEY_REBUILD:-0}" = 1 ] || ! docker image inspect "$DEY_IMAGE" >/dev/null 2>&1; then
  echo "building $DEY_IMAGE from $BASE" >&2
  docker build --build-arg "BASE=$BASE" -t "$DEY_IMAGE" "$IT_DIR" >&2
fi

# The lab signs its release like a real one, with a throwaway minisign key
# made for this run (DR-03): the lab binaries trust it next to the release
# key (install.TestPublicKey, set only here, with -X), the lab copy of
# install.sh checks it, and lib.sh signs manifest overrides with it
# (override_backend). The secret key stays on this host, outside the release
# directory the servers mount.
export DEY_LAB_KEYS="$ART/lab-key"
rm -rf "$DEY_LAB_KEYS"
mkdir -m 0700 "$DEY_LAB_KEYS"
(cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -o "$DEY_LAB_KEYS/minisign" ./scripts/minisign)
"$DEY_LAB_KEYS/minisign" keygen -pub "$DEY_LAB_KEYS/lab.pub" -sec "$DEY_LAB_KEYS/lab.key" >/dev/null
LAB_PUBKEY=$(tail -n 1 "$DEY_LAB_KEYS/lab.pub")

# The "release" the installer consumes offline: archives for this version and
# an old one (S28), SHA256SUMS, install.sh, backends.yaml and CHANGELOG.md, as
# published, with SHA256SUMS.minisig and backends.yaml.minisig by the lab key.
export DEY_DIST="$ART/release"
rm -rf "$DEY_DIST"
mkdir -p "$DEY_DIST"
case $(uname -m) in x86_64) GOARCH=amd64 ;; aarch64 | arm64) GOARCH=arm64 ;; *) GOARCH=$(uname -m) ;; esac
for v in "$DEY_VERSION" "$DEY_OLD_VERSION"; do
  d="$DEY_DIST/deyroute_${v}_linux_${GOARCH}"
  mkdir -p "$d"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -trimpath \
    -ldflags "-s -w -X main.version=$v -X main.commit=it -X github.com/localroot4/deyroute/internal/install.TestPublicKey=$LAB_PUBKEY" \
    -o "$d/deyroute" ./cmd/deyroute)
  cp "$ROOT/README.md" "$ROOT/CHANGELOG.md" "$d/"
  tar -C "$DEY_DIST" -czf "$d.tar.gz" "${d##*/}"
  rm -rf "$d"
done
cp "$ROOT/installer/install.sh" "$ROOT/internal/backend/backends.yaml" "$ROOT/CHANGELOG.md" "$DEY_DIST/"
# Only the lab copy of the installer checks the lab key (it is listed in
# SHA256SUMS, so before that is written); installer/install.sh keeps the
# release key.
sed -i "s|^MINISIGN_PUBKEY=.*|MINISIGN_PUBKEY=\"$LAB_PUBKEY\"|" "$DEY_DIST/install.sh"
grep -qxF "MINISIGN_PUBKEY=\"$LAB_PUBKEY\"" "$DEY_DIST/install.sh" || { echo "the lab install.sh has no lab key" >&2; exit 2; }
# The fake Cloudflare edge of scenario S34 (not part of a release).
(cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -trimpath -o "$DEY_DIST/it-fakecdn" ./test/integration/fakecdn)
(cd "$DEY_DIST" && sha256sum deyroute_*.tar.gz install.sh backends.yaml CHANGELOG.md >SHA256SUMS)
for f in SHA256SUMS backends.yaml; do
  MINISIGN_SECRET_KEY=$(cat "$DEY_LAB_KEYS/lab.key") "$DEY_LAB_KEYS/minisign" sign -in "$DEY_DIST/$f"
  "$DEY_LAB_KEYS/minisign" verify -in "$DEY_DIST/$f" -pubkey "$LAB_PUBKEY" >/dev/null
done

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
    # In CI, the end of the log is also an annotation of the check run, which
    # can be read without downloading the job log or the artifacts.
    if [ -n "${GITHUB_ACTIONS:-}" ]; then
      printf '::error title=%s failed on %s::%s\n' "$id" "$DISTRO" \
        "$(tail -n 40 "$ART/$id.log" | sed -e 's/%/%25/g' -e 's/\r/%0D/g' | awk '{ printf "%s%%0A", $0 }')"
    fi
    bash -c 'source "$1/lib.sh" && diagnostics "$2"' _ "$IT_DIR" "$ART/$id-diagnostics" || true
    # The warnings and errors of the daemons and units, as annotations too.
    if [ -n "${GITHUB_ACTIONS:-}" ]; then
      for d in "$ART/$id-diagnostics"/hub.txt "$ART/$id-diagnostics"/node1.txt; do
        [ -f "$d" ] || continue
        printf '::warning title=%s %s on %s::%s\n' "$id" "${d##*/}" "$DISTRO" \
          "$(grep -E '"level":"(warn|error)"|level=(WARN|ERROR)|DEY-[A-Z][0-9]{3}|Failed|failed|denied|Read-only|^Error|nft' "$d" | tail -n 40 |
            sed -e 's/%/%25/g' -e 's/\r/%0D/g' | awk '{ printf "%s%%0A", $0 }')"
      done
    fi
  fi
  [ "${DEY_KEEP:-0}" = 1 ] || dc down -v --remove-orphans >/dev/null 2>&1 || true
done
echo "results: $results  logs: $ART" >&2
[ "$failed" -eq 0 ]
