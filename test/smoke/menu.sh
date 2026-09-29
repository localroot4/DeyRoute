#!/bin/sh
# Phase 0 smoke test: the static binary runs on a bare distribution image,
# prints its version and renders the menu in an 80-column dumb terminal.
set -eu
BIN="$1"
"$BIN" version
out=$(printf '0\n' | TERM=dumb COLUMNS=80 LANG=C "$BIN" menu --once 2>&1)
echo "$out"
echo "$out" | grep -q "DEYROUTE Tunnel Manager"
echo "$out" | grep -q " 1) Dashboard (live)"
echo "$out" | grep -q "12) Settings"
echo "$out" | grep -q " 0) Exit"
# ASCII only in a non-UTF-8 terminal (scenario S24).
if printf '%s' "$out" | LC_ALL=C grep -q '[^[:print:][:space:]]'; then
	echo "non-ASCII output in dumb terminal" >&2
	exit 1
fi
echo "smoke OK"
