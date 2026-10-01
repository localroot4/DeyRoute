#!/usr/bin/env bash
# S15: full reboot of the hub and the node: every tunnel comes back UP by itself
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
serve_http node1 8443 1
serve_http node1 9443 1
T1=$(add_tunnel "$NODE1" 8443)
T2=$(add_tunnel "$NODE1" 9443)
wait_tunnel_up "$T1"
wait_tunnel_up "$T2"

docker restart "$(cid hub)" "$(cid node1)" >/dev/null
for s in hub node1; do wait_for 120 "systemd running on $s" systemd_ready "$s"; done
wait_tunnel_up "$T1" 180
wait_tunnel_up "$T2" 180
[ "$(fetch_via_hub 8443)" = "$(blob_sha node1)" ] || fail "no traffic through $T1 after the reboot"
[ "$(fetch_via_hub 9443)" = "$(blob_sha node1)" ] || fail "no traffic through $T2 after the reboot"
pass
