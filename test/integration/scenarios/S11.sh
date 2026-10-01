#!/usr/bin/env bash
# S11: the VPN service on the primary node goes down: switch to the backup node, no transport switch, event service_down
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
add_node2
serve_http node1 8443 1
serve_http node2 8443 1
T=$(add_tunnel "$NODE1" 8443 --backup "$NODE2")
wait_tunnel_up "$T"
[ "$(active_node "$T")" = "$NODE1" ] || fail "the tunnel did not start on the primary node"
tr=$(active_transport "$T")

stop_http node1 8443
on_backup() { tunnel_up "$T" && [ "$(active_node "$T")" = "$NODE2" ]; }
wait_for 90 "switch to the backup node" on_backup
[ "$(active_transport "$T")" = "$tr" ] || fail "the transport changed ($tr -> $(active_transport "$T"))"
wait_event service_down "$T" 10
has_event switch_node "$T" || fail "no switch_node event"
[ "$(fetch_via_hub 8443)" = "$(blob_sha node2)" ] || fail "traffic does not reach the backup node's service"
pass
