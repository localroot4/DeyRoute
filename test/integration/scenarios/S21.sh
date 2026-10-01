#!/usr/bin/env bash
# S21: a backend update whose probe does not pass is rolled back automatically within 60s
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "the tunnel must run on a downloaded backend (backhaul)"

setup_pair client
serve_http node1 443 1
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T" 180
dey tunnel switch "$T" --transport backhaul/tcpmux --json >/dev/null
on_bh() { tunnel_up "$T" && [ "$(active_transport "$T")" = backhaul/tcpmux ]; }
wait_for 90 "tunnel on backhaul/tcpmux" on_bh

# The "new" backhaul starts and stays active but carries nothing.
https_mirror
sha=$(fake_backend_archive backhaul $'#!/bin/sh\nexec sleep infinity')
override_backend backhaul v9.9.9 "https://$CLIENT_IP:8443/backhaul.tar.gz" "$sha"
wait_tunnel_up "$T" 60

t0=$SECONDS
out=$(dey update backends backhaul --yes --json 2>/dev/null) || true
took=$((SECONDS - t0))
log "update backends (${took}s): $out"
jq -e '.backends[] | select(.backend == "backhaul") | .status == "rolled_back"' <<<"$out" >/dev/null || fail "not rolled back"
[ "$took" -le 90 ] || fail "the update with rollback took ${took}s"
wait_event backend_update_rolled_back "" 10
wait_for 60 "tunnel back UP on backhaul" on_bh
[ "$(fetch_via_hub 443)" = "$(blob_sha node1)" ] || fail "no traffic after the rollback"
pass "${took}s"
