#!/usr/bin/env bash
# S32: manual switch between three backends (tunnel switch --transport): client outage <= 3s at each switch
# Not one of the spec's S01-S30: the phase 4 acceptance of section 16 ("manual
# switch between 3 backends, outage <= 3s, measured with a client that sends a
# request every 200ms"). The client is the real Xray VLESS+ws+tls client of
# vpn_up; the tunnel goes backhaul/wssmux -> rathole/noise -> frp/tcp -> back.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "Backhaul, Rathole and FRP"

setup_pair client
vpn_up node1 443 1
T=$(add_tunnel "$NODE1" 443 --ladder backhaul/wssmux,rathole/noise,frp/tcp)
wait_tunnel_up "$T" 180
set_failover failback false
[ "$(active_transport "$T")" = backhaul/wssmux ] || fail "the tunnel did not start on backhaul/wssmux"

outages=() times=()
for to in rathole/noise frp/tcp backhaul/wssmux; do
  from=$(active_transport "$T")
  start_prober 443
  sleep 3
  t0=$(date +%s%3N)
  dey tunnel switch "$T" --transport "$to" --json >/dev/null || fail "tunnel switch to $to failed"
  is_on() { tunnel_up "$T" && [ "$(active_transport "$T")" = "$to" ]; }
  wait_for 60 "switch to $to" is_on
  times+=($(($(date +%s%3N) - t0)))
  sleep 3
  outages+=("$(stop_prober)")
  total=$(on client sh -c 'wc -l < /tmp/prober.log')
  log "$from -> $to: UP after ${times[-1]}ms, longest client outage ${outages[-1]}ms ($total requests)"
  [ "$(fetch_via_vpn)" = "$(blob_sha node1)" ] || fail "the download through $to is corrupt"
done
for i in 0 1 2; do
  [ "${outages[i]}" -le 3000 ] || fail "switch $((i + 1)): client outage ${outages[i]}ms > 3000ms"
done
n=$(dey events --since 2h --json --tunnel "$T" | jq '[.events[] | select(.type == "switch_transport")] | length')
[ "$n" -ge 3 ] || fail "$n switch_transport events for 3 manual switches"
errs=$(vpn_tls_errors node1)
[ -z "$errs" ] || fail "TLS errors in the Xray logs:"$'\n'"$errs"
pass "outages ${outages[*]} ms, switch times ${times[*]} ms"
