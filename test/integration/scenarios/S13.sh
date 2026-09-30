#!/usr/bin/env bash
# S13: 7 blocks in a row within an hour: flapping, the tunnel stays on direct/native
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "a ladder with more than one rung"

setup_pair client
serve_http node1 443 1
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T" 180
for i in $(seq 7); do
  has_event flapping "$T" && break
  from=$(active_transport "$T")
  [ "$from" != direct/native ] || break
  port=$(tunnel_field "$T" '.rungs[] | select(.active) | .control_port')
  block --node "$NODE1_IP" tcp "$port"
  block --node "$NODE1_IP" udp "$port"
  moved() { has_event flapping "$T" || { tunnel_up "$T" && [ "$(active_transport "$T")" != "$from" ]; }; }
  wait_for 120 "block $i: move away from $from" moved
  log "block $i: $from -> $(active_transport "$T")"
done
wait_event flapping "$T" 60
settled() { tunnel_up "$T" && [ "$(active_transport "$T")" = direct/native ]; }
wait_for 90 "tunnel settles on direct/native" settled
unblock
sleep 30
[ "$(active_transport "$T")" = direct/native ] || fail "left direct/native while flapping"
# The Telegram message of this event is covered by the notify unit tests (no bot in the lab).
pass
