#!/usr/bin/env bash
# S33: wireguard/kernel and awg/userspace carry a TCP and a UDP port; tunnel delete leaves no nft rule and no interface
# Not one of the spec's S01-S30: the phase 7 acceptance of section 16. The
# containers share the host kernel: wireguard/kernel needs its WireGuard
# module (`ip link add type wireguard`) and awg/userspace needs /dev/net/tun;
# a transport the kernel cannot run is reported and left out, and the
# scenario is skipped when neither can run. The nftables ruleset and the
# interface list of hub and node are compared before the tunnel was added and
# after it was deleted.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "the amneziawg-go download"

setup_pair client
serve_http node1 8443 1
udp_echo node1 27015

transports=() missing=""
if sh_on hub "ip link add it-wgcheck type wireguard 2>/dev/null && ip link del it-wgcheck"; then
  transports+=(wireguard/kernel)
else
  missing+=" wireguard/kernel (the host kernel has no WireGuard module)"
fi
if sh_on hub "test -c /dev/net/tun" && sh_on node1 "test -c /dev/net/tun"; then
  transports+=(awg/userspace)
else
  missing+=" awg/userspace (no /dev/net/tun)"
fi
[ -z "$missing" ] || log "not tested:$missing"
[ "${#transports[@]}" -gt 0 ] || skip "the kernel can run neither WireGuard transport:$missing"

snapshot() {
  sh_on "$1" "nft list ruleset | sed -E 's/counter packets [0-9]+ bytes [0-9]+/counter/'
    echo '## links'; ip -br link | awk '{print \$1}' | sed 's/@.*//' | sort"
}
done_list=()
for tr in "${transports[@]}"; do
  before_h=$(snapshot hub) before_n=$(snapshot node1)
  out=$(dey tunnel add --node "$NODE1" --ports 8443,27015/udp --ladder "$tr" --name "wg-${tr%%/*}" --yes --json) ||
    { printf '%s\n' "$out" >&2; fail "tunnel add with $tr failed"; }
  T=$(jq -r .tunnel.id <<<"$out")
  wait_tunnel_up "$T" 120
  [ "$(active_transport "$T")" = "$tr" ] || fail "$T is UP on $(active_transport "$T"), want $tr"
  dir=$(sh_on hub "dirname \$(ls /etc/deyroute/backends/${tr%%/*}/$T/$NODE1/*/wg.json | head -1)")
  iface=$(on hub cat "$dir/wg.json" | jq -r .interface)
  for s in hub node1; do
    on "$s" ip link show dev "$iface" >/dev/null || fail "no interface $iface on $s"
  done
  [ "$(fetch_via_hub 8443)" = "$(blob_sha node1)" ] || fail "$tr: the TCP download through the tunnel is corrupt"
  reply=$(udp_ask "$HUB_IP" 27015)
  [ "$reply" = echo:it-ping ] || fail "$tr: no UDP echo through the tunnel (got '$reply')"
  log "$tr: interface $iface, TCP 8443 and UDP 27015 carried"

  dey tunnel delete "$T" --yes --json >/dev/null || fail "tunnel delete $T failed"
  gone() { ! sh_on hub "ip link show dev $iface" && ! sh_on node1 "ip link show dev $iface"; }
  wait_for 30 "interface $iface removed" gone
  sleep 3
  rc=0
  diff <(echo "$before_h") <(snapshot hub) >&2 || { log "$tr: the hub differs from before the tunnel (above)"; rc=1; }
  diff <(echo "$before_n") <(snapshot node1) >&2 || { log "$tr: the node differs from before the tunnel (above)"; rc=1; }
  [ "$rc" = 0 ] || fail "$tr: tunnel delete left nft rules or interfaces behind"
  log "$tr: tunnel delete left no nft rule and no interface"
  done_list+=("$tr")
done
pass "${done_list[*]}${missing:+; not tested:$missing}"
