#!/usr/bin/env bash
# S16: UDP closed between hub and node: hysteria2/udp and wireguard/* skipped from the ladder, yellow warning
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "the default ladder with UDP rungs"

setup_pair client
block --udp "$NODE1_IP"
serve_http node1 443 1
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T" 180
skipped=$(tunnel_json "$T" | jq -c '[.rungs[] | select(.skipped and .skipped != "") | .transport]')
log "skipped rungs: $skipped"
jq -e 'index("hysteria2/udp") != null' <<<"$skipped" >/dev/null || fail "hysteria2/udp not skipped"
jq -e 'map(select(startswith("wireguard/"))) | length > 0' <<<"$skipped" >/dev/null || fail "no wireguard rung skipped"
case $(active_transport "$T") in hysteria2/* | wireguard/*) fail "a UDP rung is active" ;; esac
has_event rung_skipped "$T" || fail "no rung_skipped event"
dey status --json | jq -e --arg t "$T" '[.warnings[]? | select(.tunnel == $t)] | length > 0' >/dev/null ||
  fail "no warning for the skipped rungs in status"
unblock
pass
