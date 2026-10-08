#!/usr/bin/env bash
# S34: front mode: with its direct path to the hub's ports cut, the node reaches the hub through a Cloudflare-like edge and a tunnel carries 20 MB intact
# The owner's commands: `deyroute front enable` on the hub, `deyroute node
# set-hub '<target>'` on the node. The edge is the fake Cloudflare of
# internal/front/fronttest (dist/it-fakecdn) on the node's loopback, reached
# by name (front.it.lab in /etc/hosts) with its CA in the system store, as a
# public CA would be (hub.front.cf_only is off: the lab edge is no
# Cloudflare address). Then everything from the node to the hub's control
# port and backend control ports is dropped: only the edge's connection to
# the hub's front port is left.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "backhaul is downloaded"
FRONT=front.it.lab

setup_pair client
serve_http node1 443 20

dey front enable --domain "$FRONT" --json >/dev/null || fail "front enable failed"
# The firewall opens the front port to Cloudflare's ranges only; the lab
# edge is the node itself.
sh_on hub "python3 - <<'PY'
import re
p = '/etc/deyroute/config.yaml'
s = open(p).read()
open(p, 'w').write(re.sub(r'(?m)^(  front:\n)', r'\1    cf_only: false\n', s, count=1))
PY"
dey config apply --json >/dev/null || fail "config apply with front.cf_only false failed"
wait_for 30 "front listening on 2053" sh_on hub "ss -Hltn 'sport = :2053' | grep -q ."
target=$(dey front status --json | jq -r .node_target)
[[ $target == "wss://$FRONT:2053/"* ]] || fail "front status gives no node target"

sh_on node1 "systemd-run --quiet --unit it-fakecdn /dist/it-fakecdn -listen 127.0.0.1:2053 -origin $HUB_IP:2053 -host $FRONT -ca /tmp/it-fakecdn.crt"
wait_for 20 "fake edge up" sh_on node1 "test -s /tmp/it-fakecdn.crt && ss -Hltn 'sport = :2053' | grep -q ."
sh_on node1 "echo '127.0.0.1 $FRONT' >> /etc/hosts && cp /tmp/it-fakecdn.crt /usr/local/share/ca-certificates/it-fakecdn.crt &&
  update-ca-certificates >/dev/null 2>&1 && systemctl restart deyroute-node"
wait_node_online "$NODE1"

dey --on node1 node set-hub "$target" --json >/dev/null || fail "node set-hub to the front failed"
on node1 nft -f - <<EOF
table inet it_cut {
  counter cut {}
  chain out { type filter hook output priority -50; ip daddr $HUB_IP tcp dport { 44433, 30000-31999 } counter name cut drop; }
}
EOF
via_front() {
  dey status --json | jq -e --arg id "$NODE1" '.nodes[] | select(.id == $id) | .online == true and .route == "front"' >/dev/null
}
wait_for 90 "node $NODE1 online through the front" via_front

T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T" 180
tr=$(active_transport "$T")
case $tr in backhaul/* | rathole/* | frp/*) ;; *) fail "a front node must use a reverse TCP rung, got $tr" ;; esac
log "tunnel $T is UP through the front via $tr"
skipped=$(dey tunnel show "$T" --json | jq -r '[.. | objects | select(.code? == "DEY-B012")] | length')
[ "$skipped" -gt 0 ] || log "note: no rung reported DEY-B012 in tunnel show"

got=$(fetch_via_hub 443)
[ "$got" = "$(blob_sha node1)" ] || fail "the 20 MB download through the front is corrupt"
direct=$(sh_on node1 "ss -Htn state established dst $HUB_IP | awk '{print \$4}' | grep -vc ':2053\$' || true")
[ "${direct:-0}" = 0 ] || fail "the node holds $direct direct connection(s) to the hub besides the edge's"
log "cut packets: $(on node1 nft -j list counter inet it_cut cut | jq '.nftables[] | select(.counter) | .counter.packets')"
dey status --json | jq -e --arg id "$NODE1" '.nodes[] | select(.id == $id) | .online == true' >/dev/null || fail "the node went offline"
pass "via $tr through the front"
