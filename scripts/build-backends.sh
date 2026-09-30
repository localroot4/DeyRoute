#!/usr/bin/env bash
# Reproducible builds of the backends whose upstream publishes no usable
# release binary (internal/backend/backends.yaml points at the DEYROUTE release
# "backend-builds" for them):
#   - amneziawg-go v1.0.4 (awg/userspace): upstream ships no binaries (QUESTIONS.md C.20)
#   - chisel v1.12.1: the tag fixes reverse-UDP return peers but has no release
#     assets upstream; built exactly like upstream's goreleaser (CGO off, -trimpath,
#     -s -w, BuildVersion) from the tagged module source (QUESTIONS.md C.29)
#
#   scripts/build-backends.sh [OUT_DIR]     (default dist/backends)
#
# The same Go toolchain, module sources (go.sum-verified through the module
# proxy) and flags give byte-identical files on any machine, so the hashes in
# the manifest can be re-checked by anyone:
#   go run ./scripts/manifest-hashes -check -only chisel,wireguard -files OUT_DIR
set -euo pipefail

OUT=${1:-dist/backends}
GO_TOOLCHAIN=go1.25.14
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
export GOTOOLCHAIN=$GO_TOOLCHAIN GOFLAGS=-mod=readonly CGO_ENABLED=0 GOOS=linux
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"

# name module version main-package-dir extra-ldflags
build() {
  local name=$1 mod=$2 ver=$3 pkg=$4 extra=$5 dir arch
  dir=$(go mod download -json "$mod@$ver" | sed -n 's/^[[:space:]]*"Dir": "\(.*\)",$/\1/p')
  [ -n "$dir" ] || { echo "cannot download $mod@$ver" >&2; exit 1; }
  for arch in amd64 arm64; do
    (cd "$dir/$pkg" && GOARCH=$arch go build -trimpath -buildvcs=false \
      -ldflags "-s -w -buildid= $extra" -o "$OUT/$name-$ver-linux-$arch" .)
  done
}

build amneziawg-go github.com/amnezia-vpn/amneziawg-go v1.0.4 . ""
build chisel github.com/jpillora/chisel v1.12.1 . "-X github.com/jpillora/chisel/share.BuildVersion=1.12.1"
(cd "$OUT" && sha256sum -- *-linux-*)
