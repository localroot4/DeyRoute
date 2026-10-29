#!/usr/bin/env bash
# Prints the DEYROUTE release signing key: the value of the line
#   const MinisignPublicKey = "RW…"
# of internal/install/keys.go (or of KEYS_GO), and fails, printing nothing,
# unless that file has exactly one such line. Comments that name a key (the
# lab's "-X …TestPublicKey=" example) are not taken for it.
#
# Used by .github/workflows/release-edge.yml to verify the signature it made
# and for the key in the release notes; tested by test/release/edge_test.sh.
#
#   bash scripts/release-pubkey.sh [KEYS_GO]
set -euo pipefail

f=${1:-$(cd "$(dirname "$0")/.." && pwd)/internal/install/keys.go}
keys=$(sed -n 's/^const MinisignPublicKey = "\(RW[A-Za-z0-9+/=]*\)"$/\1/p' "$f")
if [ -z "$keys" ] || [ "$(wc -l <<<"$keys")" != 1 ]; then
  echo "release-pubkey: want exactly one line 'const MinisignPublicKey = \"RW…\"' in $f" >&2
  exit 1
fi
printf '%s\n' "$keys"
