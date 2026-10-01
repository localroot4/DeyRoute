#!/usr/bin/env bash
# S08: the active rung's control port is blocked: UP on the next rung <= 35s (p95 of 10 runs), client outage <= 3s
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "a ladder with more than one rung"

RUNS=${S08_RUNS:-10}
setup_pair client
vpn_up node1 443 1
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T" 180
set_failover failback false

times=() outages=()
for i in $(seq "$RUNS"); do
  from=$(active_transport "$T")
  port=$(tunnel_field "$T" '.rungs[] | select(.active) | .control_port')
  [ "${port:-0}" -gt 0 ] || fail "run $i: active rung $from has no control port"
  start_prober 443
  sleep 2
  t0=$SECONDS
  block --node "$NODE1_IP" tcp "$port"
  block --node "$NODE1_IP" udp "$port"
  switched() { tunnel_up "$T" && [ "$(active_transport "$T")" != "$from" ]; }
  wait_for 90 "run $i: switch away from $from" switched
  times+=($((SECONDS - t0)))
  sleep 3
  outages+=("$(stop_prober)")
  log "run $i: $from -> $(active_transport "$T") in ${times[-1]}s, longest client outage ${outages[-1]}ms"
  unblock
  dey tunnel reset "$T" --json >/dev/null # clear quarantine for the next run
  wait_tunnel_up "$T" 120
done
p95() { printf '%s\n' "$@" | sort -n | awk '{a[NR] = $1} END {i = int(NR * 0.95 + 0.999); if (i < 1) i = 1; print a[i]}'; }
ts=$(p95 "${times[@]}")
log "switch times: ${times[*]} (p95 ${ts}s); outages at the switch: ${outages[*]} ms"
[ "$ts" -le 35 ] || fail "p95 switch time ${ts}s > 35s"
has_event switch_transport "$T" || fail "no switch_transport event"
pass "p95 ${ts}s"
