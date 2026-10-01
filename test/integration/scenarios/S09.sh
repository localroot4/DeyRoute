#!/usr/bin/env bash
# S09: the block is lifted: failback <= failback_after_s + 20s, event failback
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "a ladder with more than one rung"

setup_pair client
serve_http node1 443 1
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T" 180
FB=60
set_failover failback_after_s "$FB"
first=$(active_transport "$T")
port=$(tunnel_field "$T" '.rungs[] | select(.active) | .control_port')
block --node "$NODE1_IP" tcp "$port"
block --node "$NODE1_IP" udp "$port"
moved() { tunnel_up "$T" && [ "$(active_transport "$T")" != "$first" ]; }
wait_for 90 "switch away from $first" moved
unblock
t0=$SECONDS
back() { tunnel_up "$T" && [ "$(active_transport "$T")" = "$first" ]; }
wait_for $((FB + 60)) "failback to $first" back
took=$((SECONDS - t0))
log "failback after ${took}s (failback_after_s=$FB)"
[ "$took" -le $((FB + 20)) ] || fail "failback took ${took}s > failback_after_s + 20s"
wait_event failback "$T" 10
pass "${took}s"
