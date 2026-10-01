#!/bin/sh
# Sign the backend manifest for a release with the Go signer (scripts/minisign,
# the same one release-edge.yml uses). No-op without MINISIGN_SECRET_KEY.
set -eu
mkdir -p dist-extra
if [ -z "${MINISIGN_SECRET_KEY:-}" ]; then
	echo "sign-manifest: MINISIGN_SECRET_KEY not set, skipping"
	exit 0
fi
go run ./scripts/minisign sign -in internal/backend/backends.yaml -out dist-extra/backends.yaml.minisig
