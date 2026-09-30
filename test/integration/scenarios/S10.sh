#!/usr/bin/env bash
# S10: failback fails (blocked again at the moment of failback): immediate return, delay doubled
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
second=$(active_transport "$T")
d1=$(tunnel_field "$T" .failback_delay)
# Keep the block: the failback attempt fails and the tunnel stays on the second rung.
wait_for $((FB + 60)) "failback attempted" has_event failback_failed "$T"
[ "$(active_transport "$T")" = "$second" ] || fail "not back on $second after the failed failback"
tunnel_up "$T" || fail "tunnel not UP after the failed failback"
d2=$(tunnel_field "$T" .failback_delay)
log "failback delay ${d1}ns -> ${d2}ns"
[ "$d2" -ge $((d1 * 2)) ] || fail "the failback delay did not double (${d1} -> ${d2})"
unblock
pass
