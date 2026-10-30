#!/usr/bin/env bash
# S39: the primary node is replaced by a new one (join, tunnel edit --node, node remove): the tunnel carries traffic through the new node, the old node runs no tunnel unit and leaves @nodes
# Not one of the spec's S01-S30 (DR-23). The operator's swap of a foreign
# server: a new node joins, `tunnel edit --node NEW --yes` makes it the only
# node of the tunnel (the tunnel moves at once), `node remove OLD --yes`
# revokes the old one. Measured: the time from tunnel edit to the first
# download through the new node.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
serve_http node1 8443 1
T=$(add_tunnel "$NODE1" 8443)
wait_tunnel_up "$T"
[ "$(fetch_via_hub 8443)" = "$(blob_sha node1)" ] || fail "no download through the old node"

# The replacement joins and serves the same port.
add_node2
serve_http node2 8443 1
want=$(blob_sha node2)
t0=$(date +%s%3N)
out=$(dey tunnel edit "$T" --node "$NODE2" --yes --json) || fail "tunnel edit --node $NODE2 failed: $out"
jq -e --arg n "$NODE2" '.tunnel.nodes == [$n]' <<<"$out" >/dev/null || fail "tunnel edit did not set the node list to $NODE2: $out"
on_new() { tunnel_up "$T" && [ "$(active_node "$T")" = "$NODE2" ] && [ "$(fetch_via_hub 8443 || true)" = "$want" ]; }
wait_for 120 "download through the new node" on_new
ms=$(($(date +%s%3N) - t0))
log "tunnel edit to the first download through the new node: ${ms}ms"

# The old node goes: it is in no tunnel any more, so the removal is not
# refused (DEY-C008), and its tunnel units stop.
rm_out=$(dey node remove "$NODE1" --yes --json) || fail "node remove $NODE1 failed: $rm_out"
jq -e --arg n "$NODE1" '.ok == true and .node == $n' <<<"$rm_out" >/dev/null || fail "node remove: $rm_out"
[ "$(dey node list --json | jq -c '[.nodes[].id]')" = "[\"$NODE2\"]" ] || fail "node list after the removal: $(dey node list --json)"
old_units() { on node1 systemctl list-units --all --plain --no-legend 'deyroute-tun@*' | awk '$3 == "active"'; }
no_old_units() { [ -z "$(old_units)" ]; }
wait_for 30 "no active tunnel unit on the old node" no_old_units
in_nodes() { on hub nft list set inet deyroute nodes | contains "$NODE1_IP"; }
not_in_nodes() { ! in_nodes; }
wait_for 30 "the old node's IP out of @nodes" not_in_nodes

# The tunnel keeps carrying traffic through the new node.
tunnel_up "$T" || fail "tunnel $T left UP after node remove ($(tunnel_field "$T" .state))"
[ "$(fetch_via_hub 8443)" = "$want" ] || fail "no download through the new node after node remove"
pass "tunnel edit to traffic through the new node in ${ms}ms"
