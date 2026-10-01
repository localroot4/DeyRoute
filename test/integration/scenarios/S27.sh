#!/usr/bin/env bash
# S27: the node's public IP changes: it reconnects from the new IP, @nodes updated, event node_ip_changed
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
serve_http node1 8443 1
T=$(add_tunnel "$NODE1" 8443)
wait_tunnel_up "$T"
NEW=10.77.0.21
net="${DEY_PROJECT}_deynet"
docker network disconnect "$net" "$(cid node1)"
docker network connect --ip "$NEW" "$net" "$(cid node1)"

wait_event node_ip_changed "" 90
ip_now() { [ "$(dey node list --json | jq -r --arg id "$NODE1" '.nodes[] | select(.id == $id) | .public_ip')" = "$NEW" ]; }
wait_for 30 "node public_ip updated" ip_now
in_set() { on hub nft list ruleset | grep -A3 'set nodes' | grep -q "$NEW"; }
wait_for 30 "@nodes contains $NEW" in_set
on hub nft list ruleset | grep -A3 'set nodes' | grep -q "$NODE1_IP" && fail "the old IP is still in @nodes"
wait_tunnel_up "$T" 120
[ "$(fetch_via_hub 8443)" = "$(blob_sha node1)" ] || fail "no traffic after the IP change"
pass
