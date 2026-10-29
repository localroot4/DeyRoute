#!/usr/bin/env bash
# S37: nft flush ruleset on the hub and on a node with NAT rules: table inet deyroute comes back within 70 s, the tunnel stays UP
# Not one of the spec's S01-S30 (DR-14). Hub: with the default
# security.firewall_reassert_s (60 s) the flushed table is back within 70 s,
# event firewall_reapplied (reason missing) is recorded, and the tunnel stays
# UP and carries traffic throughout (its rung needs no NAT). Node: a
# wireguard/kernel rung puts DNAT rules into the node's table; after a flush
# on the node the table comes back within 70 s, the node logs
# firewall_reapplied, and the tunnel is UP on the same rung again. The node
# checks every 10 s and the tunnel tolerates 40 s of failed probes, so the
# NAT outage the flush causes does not switch the rung. The node part needs
# the host kernel's WireGuard module (like S33) and is skipped without it.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
serve_http node1 8443 1

table_on() { on "$1" nft list table inet deyroute | grep -q "$2"; }
# up_through SECONDS SERVICE PATTERN TUNNEL: the table of SERVICE holds
# PATTERN again within SECONDS, and TUNNEL is UP at every look meanwhile.
up_through() {
  local t=$1 s=$2 pat=$3 tun=$4 end=$((SECONDS + $1))
  until table_on "$s" "$pat" 2>/dev/null; do
    [ "$SECONDS" -lt "$end" ] || fail "table inet deyroute not back on $s within ${t}s"
    tunnel_up "$tun" || fail "tunnel $tun left UP while the table on $s was gone ($(tunnel_field "$tun" .state))"
    sleep 1
  done
}

# Hub.
T=$(add_tunnel "$NODE1" 8443)
wait_tunnel_up "$T"
table_on hub "set nodes" || fail "no table inet deyroute on the hub"
start=$SECONDS
on hub nft flush ruleset
on hub nft list table inet deyroute >/dev/null 2>&1 && fail "the flush left table inet deyroute on the hub"
up_through 70 hub "set nodes" "$T"
log "hub: table back after $((SECONDS - start)) s"
table_on hub "$NODE1_IP" || fail "the restored hub table has no @nodes entry for $NODE1_IP"
wait_event firewall_reapplied "" 15
dey events --since 2h --json | jq -e '.events[] | select(.type == "firewall_reapplied" and .reason == "missing")' >/dev/null ||
  fail "firewall_reapplied has no reason missing"
tunnel_up "$T" || fail "tunnel $T is not UP after the hub flush"
[ "$(fetch_via_hub 8443)" = "$(blob_sha node1)" ] || fail "no traffic after the hub flush"
dey tunnel delete "$T" --yes --json >/dev/null || fail "tunnel delete $T failed"

# Node with a NAT rung.
if ! sh_on node1 "ip link add it-wgcheck type wireguard 2>/dev/null && ip link del it-wgcheck"; then
  pass "hub; node not tested: the host kernel has no WireGuard module"
fi
sh_on node1 "if grep -q '^security:' /etc/deyroute/config.yaml; then
    sed -i '/^security:/a\\  firewall_reassert_s: 10' /etc/deyroute/config.yaml
  else
    printf 'security:\n  firewall_managed: true\n  firewall_reassert_s: 10\n' >>/etc/deyroute/config.yaml
  fi
  systemctl restart deyroute-node"
wait_for 60 "node $NODE1 back online" node_online "$NODE1"
out=$(dey tunnel add --node "$NODE1" --ports 8443 --ladder wireguard/kernel --name wg-flush --yes --json) ||
  { printf '%s\n' "$out" >&2; fail "tunnel add with wireguard/kernel failed"; }
T=$(jq -r .tunnel.id <<<"$out")
set_failover fail_threshold 8
wait_tunnel_up "$T" 120
[ "$(active_transport "$T")" = wireguard/kernel ] || fail "$T is UP on $(active_transport "$T"), want wireguard/kernel"
table_on node1 "chain prerouting" || fail "no NAT rule in the node's table inet deyroute"
start=$SECONDS
on node1 nft flush ruleset
wait_for 70 "table inet deyroute back on node1" table_on node1 "chain prerouting"
log "node: table back after $((SECONDS - start)) s"
on node1 grep -q '"event":"firewall_reapplied"' /var/log/deyroute/node.log || fail "the node did not log firewall_reapplied"
wait_tunnel_up "$T" 60
[ "$(active_transport "$T")" = wireguard/kernel ] || fail "the flush on the node switched $T to $(active_transport "$T")"
[ "$(fetch_via_hub 8443)" = "$(blob_sha node1)" ] || fail "no traffic after the node flush"
pass
