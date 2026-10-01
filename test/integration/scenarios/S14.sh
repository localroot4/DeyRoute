#!/usr/bin/env bash
# S14: deyroute-hub restarts in the middle of a switch: state recovered, the healthy tunnel is not restarted
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
add_node2
serve_http node1 8443 1
serve_http node2 8443 1
serve_http node2 9443 1
T1=$(add_tunnel "$NODE2" 9443)
T2=$(add_tunnel "$NODE1" 8443 --backup "$NODE2")
wait_tunnel_up "$T1"
wait_tunnel_up "$T2"
u1=$(unit_of "$T1")
hub_pid=$(main_pid hub "$u1") node_pid=$(main_pid node2 "$u1")

block --host "$NODE1_IP"
leaving() { [ "$(tunnel_field "$T2" .state)" != UP ]; }
wait_for 60 "$T2 starts switching" leaving
log "$T2 is $(tunnel_field "$T2" .state); restarting deyroute-hub"
on hub systemctl restart deyroute-hub
on_backup() { tunnel_up "$T2" && [ "$(active_node "$T2")" = "$NODE2" ]; }
wait_for 120 "$T2 recovered on the backup node" on_backup
tunnel_up "$T1" || fail "$T1 is not UP after the hub restart"
[ "$(main_pid hub "$u1")" = "$hub_pid" ] || fail "the healthy tunnel's hub unit was restarted"
[ "$(main_pid node2 "$u1")" = "$node_pid" ] || fail "the healthy tunnel's node unit was restarted"
[ "$(fetch_via_hub 9443)" = "$(blob_sha node2)" ] || fail "the healthy tunnel does not carry traffic"
unblock
pass
