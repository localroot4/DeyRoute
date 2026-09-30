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
[ "$(tunnel_field "$T" .state)" = UP ] || fail "tunnel not UP after the crashes"
pass
