#!/usr/bin/env bash
# S04: tunnel on 443 (Backhaul first when online) with a real VLESS+ws+tls client: 100 MB intact, no TLS error, the user is masked by the hub
# Online, the client is a real Xray VLESS+ws+tls client that dials hub:443
# and the node runs a real Xray server behind the tunnel (lib.sh vpn_up); the
# 100 MB file comes from the web server on the node's loopback through it.
# "User IP = hub IP" (spec 17) is checked as: the client only ever talks to
# the hub, the VPN service on the node never sees the client's address (only
# the tunnel's), and the user's traffic leaves the VPN at the node.
# Offline (no Xray download) only the 100 MB download through the tunnel is
# checked.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
vpn_up node1 443 100
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T"
tr=$(active_transport "$T")
log "tunnel $T is UP via $tr"
if [ "$DEY_OFFLINE" = 1 ]; then
  got=$(fetch_via_vpn)
  [ "$got" = "$(blob_sha node1)" ] || fail "the 100 MB download through the tunnel is corrupt"
  pass "via $tr, offline: no VPN client"
fi
case $tr in backhaul/*) ;; *) fail "expected the first rung (backhaul/*) to carry the tunnel, got $tr" ;; esac

# Count what the client sends to the node directly (it must be nothing).
on client nft -f - <<EOF
table inet it_watch {
  counter to_node {}
  chain out { type filter hook output priority -60; ip daddr $NODE1_IP counter name to_node; }
}
EOF
t0=$(date +%s%3N)
got=$(fetch_via_vpn)
ms=$(($(date +%s%3N) - t0))
[ "$got" = "$(blob_sha node1)" ] || fail "the 100 MB download through the VPN client is corrupt"
log "100 MB through the Xray client and $tr in ${ms}ms"
direct=$(on client nft -j list counter inet it_watch to_node | jq '.nftables[] | select(.counter) | .counter.packets')
[ "$direct" = 0 ] || fail "the client sent $direct packet(s) to the node directly"

# The exit: a request through the VPN to a web server on the client's network
# arrives from the node.
it_unit client whoami "/usr/bin/python3 -m http.server 8081 --bind $CLIENT_IP --directory /tmp"
wait_for 20 "whoami server" sh_on client "ss -Hltn 'sport = :8081' | grep -q ."
sh_on client "curl -fsS -o /dev/null --max-time 10 $VPN_CURL http://$CLIENT_IP:8081/" ||
  fail "no request through the VPN to the client network"
seen=$(sh_on client "journalctl --no-pager -q -u it-whoami | grep -o -E '[0-9.]+ - - ' | cut -d' ' -f1 | sort -u")
log "the exit seen on the internet: ${seen//$'\n'/ }"
[ "$seen" = "$NODE1_IP" ] || fail "the user's traffic left the VPN from '$seen', want the node $NODE1_IP"

from=$(sh_on node1 "grep -o -E 'from (tcp:)?[0-9.]+' /var/log/it-xray-access.log | sed -E 's/from (tcp:)?//' | sort -u")
[ -n "$from" ] || fail "the VPN server on the node logged no connection"
log "the VPN service on the node sees the user as: ${from//$'\n'/ }"
! grep -qx "$CLIENT_IP" <<<"$from" || fail "the VPN service on the node sees the client's own address"
errs=$(vpn_tls_errors node1)
[ -z "$errs" ] || fail "TLS errors in the Xray logs:"$'\n'"$errs"
pass "via $tr, 100 MB in ${ms}ms, the service sees ${from//$'\n'/ }"
