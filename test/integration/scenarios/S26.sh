#!/usr/bin/env bash
# S26: grep every secret in all logs and in the doctor output: no token or key is found
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

up hub node1 node2 client
install_hub hub ir-1 >&2
l1=$(join_link) l2=$(join_link) unused=$(join_link)
install_node node1 "$l1" >&2
install_node node2 "$l2" >&2
NODE1=$(node_id_of node1)
wait_node_online "$NODE1"
serve_http node1 8443 1
T=$(add_tunnel "$NODE1" 8443)
wait_tunnel_up "$T"
dey tunnel restart "$T" --json >/dev/null
wait_tunnel_up "$T"
dey security rotate-tokens --tunnel "$T" --yes --json >/dev/null
wait_tunnel_up "$T"
dey doctor --out /root/doctor-hub.tar.gz --json >/dev/null
dey doctor --node "$NODE1" --out /root/doctor-node.tar.gz --json >/dev/null

list=$(mktemp)
{
  secrets_of hub
  secrets_of node1
  secrets_of node2
  join_token "$l1"; join_token "$l2"; join_token "$unused"
} | sort -u >"$list"
log "$(wc -l <"$list") secret strings to look for"
[ -s "$list" ] || fail "no secrets collected"

found=0
for s in hub node1 node2; do
  hits=$(sh_on "$s" "mkdir -p /tmp/s26 && cd /tmp/s26 && for f in /root/doctor-*.tar.gz; do [ -f \"\$f\" ] && tar -xzf \"\$f\"; done 2>/dev/null
    { journalctl --no-pager -u 'deyroute*' 2>/dev/null; deyroute status 2>&1; deyroute logs hub 2>&1 | tail -n 500; } > /tmp/s26/cli-output.txt
    grep -rlF -f /dev/stdin /var/log/deyroute /tmp/s26 2>/dev/null; true" <"$list")
  if [ -n "$hits" ]; then log "secrets found on $s in: $hits"; found=1; fi
done
rm -f "$list"
[ "$found" = 0 ] || fail "secrets leaked into logs or doctor output"
pass
