#!/usr/bin/env bash
# S04: tunnel on 443 (Backhaul first when online) carries a 100 MB download intact
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
serve_http node1 443 100
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T"
tr=$(active_transport "$T")
log "tunnel $T is UP via $tr"
if [ "$DEY_OFFLINE" != 1 ]; then
  case $tr in backhaul/*) ;; *) fail "expected the first rung (backhaul/*) to carry the tunnel, got $tr" ;; esac
fi
got=$(fetch_via_hub 443)
[ "$got" = "$(blob_sha node1)" ] || fail "the 100 MB download through the tunnel is corrupt"
pass "via $tr"
