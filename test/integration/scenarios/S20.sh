#!/usr/bin/env bash
# S20: a backend update whose download has the wrong sha256 is refused (DEY-S001), nothing changes
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
serve_http node1 8443 1
T=$(add_tunnel "$NODE1" 8443)
wait_tunnel_up "$T"
unit=$(unit_of "$T")
hp=$(main_pid hub "$unit") np=$(main_pid node1 "$unit")

https_mirror
fake_backend_archive backhaul $'#!/bin/sh\nexec sleep infinity' >/dev/null
override_backend backhaul v9.9.9 "https://$CLIENT_IP:8443/backhaul.tar.gz" "$(printf '0%.0s' $(seq 64))"

before=$(sh_on hub "ls -R /var/lib/deyroute/backends 2>/dev/null; true")
before_n=$(sh_on node1 "ls -R /var/lib/deyroute/backends 2>/dev/null; true")
out=$(dey update backends backhaul --yes --json 2>/dev/null) || true
log "update backends: $out"
jq -e '[.backends[]?, .error? | select(. != null) | (.error.code? // .code?)] | index("DEY-S001") != null' <<<"$out" >/dev/null ||
  fail "DEY-S001 not reported"
[ "$(sh_on hub "ls -R /var/lib/deyroute/backends 2>/dev/null; true")" = "$before" ] || fail "hub backends changed"
[ "$(sh_on node1 "ls -R /var/lib/deyroute/backends 2>/dev/null; true")" = "$before_n" ] || fail "node backends changed"
{ [ "$(main_pid hub "$unit")" = "$hp" ] && [ "$(main_pid node1 "$unit")" = "$np" ]; } || fail "the active candidate was restarted"
tunnel_up "$T" || fail "tunnel not UP"
pass
