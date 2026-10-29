#!/usr/bin/env bash
# S42: Hysteria2 port hopping is scoped to the hub: a UDP datagram to :20500 on the node from another source is not redirected
# DR-18. With advanced.hysteria_port_hopping the node's table inet deyroute
# redirects 20000-20999 to the rung's port only for datagrams from the
# hub's public address to the node's public address (its own here, so the
# destination stays in the rule). The tunnel carries traffic over the
# hopping range; a datagram from the client to NODE1_IP:20500 reaches the
# node with its original port (not DNATed), one from the hub is DNATed.
# Counted in an input chain of the node's own test table.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

needs_online "hysteria2 is downloaded"
setup_pair client
serve_http node1 443 1
T=$(add_tunnel "$NODE1" 443 --ladder hysteria2/udp,direct/native)
wait_tunnel_up "$T" 180
[ "$(active_transport "$T")" = hysteria2/udp ] || fail "active transport $(active_transport "$T"), want hysteria2/udp"

# Port hopping on (no CLI flag: config.yaml, as the owner would edit it).
sh_on hub "python3 - <<'PY'
p = '/etc/deyroute/config.yaml'
s = open(p).read()
k = '  - id: $T\n'
assert k in s and 'hysteria_port_hopping' not in s
open(p, 'w').write(s.replace(k, k + '    advanced:\n      hysteria_port_hopping: true\n', 1))
PY"
dey config apply --json >/dev/null || fail "config apply with port hopping failed"
rule="ip saddr $HUB_IP ip daddr $NODE1_IP udp dport 20000-20999 redirect to :"
wait_for 90 "scoped hopping rule on the node" sh_on node1 "nft list chain inet deyroute prerouting | grep -qF '$rule'"
if sh_on node1 "nft list chain inet deyroute prerouting" | grep -E 'dport 20000-20999' | grep -vqF "ip saddr $HUB_IP"; then
  fail "an unscoped hopping rule is on the node: $(on node1 nft list chain inet deyroute prerouting)"
fi
sh_on hub "grep -rqs ':20000-20999' /etc/deyroute/backends/hysteria2" || fail "the hub does not dial the hopping range"
wait_tunnel_up "$T" 120
[ "$(active_transport "$T")" = hysteria2/udp ] || fail "active transport $(active_transport "$T") after hopping, want hysteria2/udp"
sum=$(blob_sha node1)
fetched() { [ "$(fetch_via_hub 443 || true)" = "$sum" ]; }
wait_for 60 "download through the hopping tunnel" fetched

# Count, after the DNAT, what the node receives for :20500.
on node1 nft -f - <<EOF
table inet it_hop {
  counter client_plain {}
  counter client_dnat {}
  counter hub_dnat {}
  chain in {
    type filter hook input priority 0; policy accept;
    ip saddr $CLIENT_IP udp dport 20500 counter name client_plain
    ip saddr $CLIENT_IP ct status dnat counter name client_dnat
    ip saddr $HUB_IP ct status dnat ct original proto-dst 20500 counter name hub_dnat
  }
}
EOF
hop_count() { on node1 nft -j list counter inet it_hop "$1" | jq '.nftables[] | select(.counter) | .counter.packets'; }
send_20500() {
  on "$1" python3 - "$NODE1_IP" <<'PY'
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
for _ in range(3):
    s.sendto(b"it-hop", (sys.argv[1], 20500))
PY
}
send_20500 client
send_20500 hub
sleep 1
plain=$(hop_count client_plain) dnat=$(hop_count client_dnat) hub=$(hop_count hub_dnat)
log "client: $plain datagrams on :20500, $dnat DNATed; hub: $hub DNATed"
[ "$plain" -ge 3 ] || fail "the client's datagrams to :20500 did not arrive unchanged ($plain)"
[ "$dnat" = 0 ] || fail "the client's datagrams were redirected ($dnat)"
[ "$hub" -ge 1 ] || fail "the hub's datagrams to :20500 were not redirected ($hub)"
on node1 nft delete table inet it_hop
pass
