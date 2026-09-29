#!/bin/sh
# Sign the backend manifest for a release. No-op without MINISIGN_KEY_FILE.
set -eu
mkdir -p dist-extra
if [ -z "${MINISIGN_KEY_FILE:-}" ]; then
	echo "sign-manifest: MINISIGN_KEY_FILE not set, skipping"
	exit 0
fi
printf '%s\n' "${MINISIGN_PASSWORD:-}" | minisign -S -s "$MINISIGN_KEY_FILE" \
	-m internal/install/backends.yaml -x dist-extra/backends.yaml.minisig
