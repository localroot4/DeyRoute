#!/usr/bin/env bash
# S07: kill -9 of the backend process on the hub / the node: back in < 5s, event backend_crash
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
serve_http node1 8443 1
T=$(add_tunnel "$NODE1" 8443)
wait_tunnel_up "$T"
unit=$(unit_of "$T")
{ [ -n "$unit" ] && [ "$unit" != null ]; } || fail "no active unit for $T"
rung=$(tunnel_json "$T" | jq -c '[.active_node, .active_transport]')
switches() { dey events --since 2h --json --tunnel "$T" | jq '[.events[] | select(.type | test("^switch_|^failback"))] | length'; }
sw0=$(switches)

for s in hub node1; do
  pid=$(main_pid "$s" "$unit")
  [ "${pid:-0}" -gt 0 ] || fail "no main pid for $unit on $s"
  t0=$(date +%s%N)
  on "$s" kill -9 "$pid"
  # Back = a new main pid and traffic flows again.
  back() { p=$(main_pid "$s" "$unit"); [ "${p:-0}" -gt 0 ] && [ "$p" != "$pid" ] && [ "$(fetch_via_hub 8443)" = "$(blob_sha node1)" ]; }
  wait_for 30 "$unit back on $s" back
  ms=$((($(date +%s%N) - t0) / 1000000))
  log "$s: back after ${ms}ms"
  [ "$ms" -lt 5000 ] || fail "$unit on $s came back after ${ms}ms (limit 5000ms)"
done
wait_event backend_crash "$T" 30
# The tunnel recovers by itself, on the same rung, without any switch. Its
# state follows the path probe, taken every probe_interval_s (section 9):
# a probe that fell into an outage left it DEGRADED, and only the next probe
# turns it UP again (DEGRADED -> UP on one passing probe). So the next probe
# cycle (interval + timeout, plus a second for the read) is waited for
# instead of reading the state once at an arbitrary point of that cycle.
cycle=$(tunnel_json "$T" | jq '.failover.probe_interval_s + .failover.probe_timeout_s + 1')
log "state right after the crashes: $(tunnel_json "$T" | jq -c '[.state, .active_node, .active_transport]')"
same_rung_up() { tunnel_up "$T" && [ "$(tunnel_json "$T" | jq -c '[.active_node, .active_transport]')" = "$rung" ]; }
wait_for "$cycle" "tunnel UP on $rung again after the crashes" same_rung_up
[ "$(switches)" = "$sw0" ] || fail "the crashes caused a switch: $(dey events --since 2h --json --tunnel "$T" | jq -c '[.events[] | select(.type | test("^switch_|^failback")) | [.type, .reason]]')"
pass
