#!/usr/bin/env bash
# S16: UDP closed between hub and node: hysteria2/udp, backhaul/udp and wireguard/* skipped from the ladder, yellow warning
# The default (TCP) ladder has hysteria2/udp; the UDP-only ladder
# (udp-default) has backhaul/udp and hysteria2/udp. wireguard/kernel left
# udp-default after the field report of 2026-10-17 and is optional, so a
# UDP-only tunnel on a ladder of the owner's that lists it checks it.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "the default ladders with UDP rungs"

setup_pair client
block --udp "$NODE1_IP"
serve_http node1 443 1
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T" 180
skipped_of() { tunnel_json "$1" | jq -c '[.rungs[] | select(.skipped and .skipped != "") | .transport]'; }
s1=$(skipped_of "$T")
log "skipped rungs of the TCP tunnel: $s1"
jq -e 'index("hysteria2/udp") != null' <<<"$s1" >/dev/null || fail "hysteria2/udp not skipped"
case $(active_transport "$T") in hysteria2/* | wireguard/*) fail "a UDP rung is active" ;; esac
has_event rung_skipped "$T" || fail "no rung_skipped event"
dey status --json | jq -e --arg t "$T" '[.warnings[]? | select(.tunnel == $t)] | length > 0' >/dev/null ||
  fail "no warning for the skipped rungs in status"

# A UDP-only tunnel cannot come up without UDP; its plan must still skip
# the UDP rungs with the warning instead of trying them. udp-default has no
# wireguard rung any more.
dey tunnel add --node "$NODE1" --ports 27015/udp --name games --yes --json >/dev/null 2>&1 || true
s2=$(skipped_of games)
log "skipped rungs of the UDP tunnel: $s2"
jq -e 'index("backhaul/udp") != null' <<<"$s2" >/dev/null || fail "backhaul/udp not skipped on the UDP tunnel"
jq -e 'index("hysteria2/udp") != null' <<<"$s2" >/dev/null || fail "hysteria2/udp not skipped on the UDP tunnel"
tunnel_json games | jq -e '[.rungs[] | select(.transport | startswith("wireguard/"))] | length == 0' >/dev/null ||
  fail "udp-default still has a wireguard rung"

# The owner's ladder with wireguard/kernel: that rung is skipped as well.
dey ladder create udp-wg --rungs backhaul/udp,hysteria2/udp,wireguard/kernel,direct/native >/dev/null
dey tunnel add --node "$NODE1" --ports 27016/udp --name games-wg --ladder udp-wg --yes --json >/dev/null 2>&1 || true
s3=$(skipped_of games-wg)
log "skipped rungs of the UDP tunnel on udp-wg: $s3"
jq -e 'index("wireguard/kernel") != null' <<<"$s3" >/dev/null || fail "wireguard/kernel not skipped on the udp-wg tunnel"
unblock
pass
