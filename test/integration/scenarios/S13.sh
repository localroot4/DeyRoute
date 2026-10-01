#!/usr/bin/env bash
# S13: 7 blocks in a row within an hour: flapping, the tunnel stays on direct/native
# Each round blocks the active rung until the tunnel moves, then lifts the
# block so the failback to rung 1 comes quickly; every move counts towards
# max_switches_per_hour (spec section 9) until the tunnel is held as flapping.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "a ladder with more than one rung"

setup_pair client
serve_http node1 443 1
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T" 180
set_failover failback_after_s 20
first=$(active_transport "$T")
for i in $(seq 10); do
  has_event flapping "$T" && break
  from=$(active_transport "$T")
  if [ "$from" != direct/native ]; then
    port=$(tunnel_field "$T" '.rungs[] | select(.active) | .control_port')
    block --node "$NODE1_IP" tcp "$port"
    block --node "$NODE1_IP" udp "$port"
    moved() { has_event flapping "$T" || { tunnel_up "$T" && [ "$(active_transport "$T")" != "$from" ]; }; }
    wait_for 120 "block $i: move away from $from" moved
    log "block $i: $from -> $(active_transport "$T")"
    unblock
  fi
  has_event flapping "$T" && break
  back() { has_event flapping "$T" || { tunnel_up "$T" && [ "$(active_transport "$T")" = "$first" ]; }; }
  wait_for 120 "round $i: failback to $first or flapping" back
done
wait_event flapping "$T" 60
settled() { tunnel_up "$T" && [ "$(active_transport "$T")" = direct/native ]; }
wait_for 120 "tunnel held on direct/native" settled
sleep 40
[ "$(active_transport "$T")" = direct/native ] || fail "left direct/native while flapping"
# The Telegram message of this event is covered by the notify unit tests (no bot in the lab).
pass
