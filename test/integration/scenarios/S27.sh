#!/usr/bin/env bash
# S27: the node's public IP changes: it reconnects from the new IP, @nodes updated, event node_ip_changed (while the control port is hammered)
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
serve_http node1 8443 1
T=$(add_tunnel "$NODE1" 8443)
wait_tunnel_up "$T"
# DR-15: meanwhile the client hammers the control port with 2 new
# connections per second. Unknown sources are limited per address (3 per
# minute each, 30 in all), so the hammer uses up only its own budget and
# the node reconnects from its new address all the same.
# The join window of setup admits every source; close it, so unknown
# sources go through the per-source limit.
dey node join-revoke --json >/dev/null || fail "join-revoke failed"
docker exec -d "$(cid client)" bash -c "while :; do (timeout 2 bash -c 'exec 3<>/dev/tcp/$HUB_IP/$CONTROL_PORT' >/dev/null 2>&1 &); sleep 0.5; done"
hammered() { on hub nft list set inet deyroute ctl_unknown4 | contains "$CLIENT_IP"; }
wait_for 30 "the client's hammer reaches the per-source limit" hammered
NEW=10.77.0.21
net="${DEY_PROJECT}_deynet"
docker network disconnect "$net" "$(cid node1)"
docker network connect --ip "$NEW" "$net" "$(cid node1)"

wait_event node_ip_changed "" 90
ip_now() { [ "$(dey node list --json | jq -r --arg id "$NODE1" '.nodes[] | select(.id == $id) | .public_ip')" = "$NEW" ]; }
wait_for 30 "node public_ip updated" ip_now
in_set() { on hub nft list ruleset | grep -A3 'set nodes' | contains "$NEW"; }
wait_for 30 "@nodes contains $NEW" in_set
on hub nft list ruleset | grep -A3 'set nodes' | contains "$NODE1_IP" && fail "the old IP is still in @nodes"
wait_tunnel_up "$T" 120
[ "$(fetch_via_hub 8443)" = "$(blob_sha node1)" ] || fail "no traffic after the IP change"
hammered || fail "the hammer stopped before the end"
pass
