#!/usr/bin/env bash
# S19: updating deyroute loses no traffic on the active tunnel
# The update replaces the binary and restarts deyroute-hub / deyroute-node while the
# backend units keep running. Offline, the new release is installed with the
# installer (the path `deyroute update` shares after download and verification;
# the signed download itself is covered by the install unit tests).
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

up hub node1 client
OLD=/dist/deyroute_${DEY_OLD_VERSION}_linux_${ARCH}.tar.gz
on hub bash /dist/install.sh --local "$OLD" --role hub --name ir-1 --yes >&2
on node1 bash /dist/install.sh --local "$OLD" join "$(join_link)" >&2
NODE1=$(node_id_of node1)
wait_node_online "$NODE1"
serve_http node1 8443 1
T=$(add_tunnel "$NODE1" 8443)
wait_tunnel_up "$T"
unit=$(unit_of "$T")
hp=$(main_pid hub "$unit") np=$(main_pid node1 "$unit")

start_prober 8443
sleep 3
on hub bash /dist/install.sh --local "$ARCHIVE" >&2
sleep 5
on node1 bash /dist/install.sh --local "$ARCHIVE" >&2
wait_node_online "$NODE1" 90
sleep 5
out=$(stop_prober)
total=$(on client sh -c 'wc -l < /tmp/prober.log')
fails=$(on client grep -c ' fail$' /tmp/prober.log || true)
log "requests: $total, failed: ${fails:-0}, longest outage ${out}ms"
[ "$(dey version --json | jq -r .version)" = "$DEY_VERSION" ] || fail "hub not updated"
[ "$(dey --on node1 version --json | jq -r .version)" = "$DEY_VERSION" ] || fail "node not updated"
[ "${fails:-0}" = 0 ] || fail "$fails request(s) failed during the update"
[ "$(main_pid hub "$unit")" = "$hp" ] || fail "the hub backend unit was restarted by the update"
[ "$(main_pid node1 "$unit")" = "$np" ] || fail "the node backend unit was restarted by the update"
tunnel_up "$T" || fail "tunnel not UP after the update"
pass
