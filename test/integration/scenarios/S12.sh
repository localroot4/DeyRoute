#!/usr/bin/env bash
# S12: the primary node loses its network completely: the backup node carries the tunnel within 45s
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
add_node2
serve_http node1 8443 1
serve_http node2 8443 1
T=$(add_tunnel "$NODE1" 8443 --backup "$NODE2")
wait_tunnel_up "$T"
[ "$(active_node "$T")" = "$NODE1" ] || fail "the tunnel did not start on the primary node"

t0=$SECONDS
block --host "$NODE1_IP"
on_backup() { tunnel_up "$T" && [ "$(active_node "$T")" = "$NODE2" ]; }
wait_for 120 "backup node active" on_backup
took=$((SECONDS - t0))
log "backup node active after ${took}s"
[ "$took" -le 45 ] || fail "backup node took ${took}s (limit 45s)"
[ "$(fetch_via_hub 8443)" = "$(blob_sha node2)" ] || fail "traffic does not reach the backup node"
unblock
pass "${took}s"
