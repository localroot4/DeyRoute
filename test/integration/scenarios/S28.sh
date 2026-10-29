#!/usr/bin/env bash
# S28: a node with an incompatible version: DEY-N004, then it follows the hub's version by itself
# DR-11: the hub sends self.update to a node of another major.minor as soon
# as it connects, without a `deyroute update` before. The node installs the
# signed lab release the hub kept (DR-03), comes back compatible, and a
# tunnel then comes UP on it. That commands are refused while the node is
# incompatible is covered by the unit tests TestIncompatibleNode (hub) and
# TestIncompatibleHubRefusesCommands (node): the automatic update races
# such checks here.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

up hub node1
install_hub hub ir-1 >&2
OLD=/dist/deyroute_${DEY_OLD_VERSION}_linux_${ARCH}.tar.gz
on node1 bash /dist/install.sh --local "$OLD" join "$(join_link)" >&2 || fail "the old node could not join"
NODE1=$(node_id_of node1)

# The incompatible node is reported (hub.log keeps it: the status warning
# may be gone already when the node has followed).
n004() { on hub grep -q 'DEY-N004' /var/log/deyroute/hub.log; }
wait_for 60 "DEY-N004 for the old node in hub.log" n004
pushed() { on hub grep -q 'node runs another major.minor than the hub' /var/log/deyroute/hub.log; }
wait_for 30 "the hub pushes its version to the incompatible node" pushed

follows() {
  dey node list --json | jq -e --arg id "$NODE1" --arg v "$DEY_VERSION" \
    '.nodes[] | select(.id == $id) | .online and .compatible and .version == $v' >/dev/null
}
wait_for 180 "the node follows the hub to $DEY_VERSION without deyroute update" follows
log "node: $(dey node list --json | jq -c --arg id "$NODE1" '.nodes[] | select(.id == $id) | {version, compatible, online}')"
[ "$(dey --on node1 version --json | jq -r .version)" = "$DEY_VERSION" ] || fail "the node binary is not $DEY_VERSION"
if on hub grep -q "node could not install the hub's version" /var/log/deyroute/hub.log; then
  fail "a self.update of the node failed: $(on hub grep "node could not install the hub's version" /var/log/deyroute/hub.log)"
fi
w=$(dey status --json | jq -c '[.warnings[]? | select(.code == "DEY-N004")]')
jq -e 'length == 0' <<<"$w" >/dev/null || fail "DEY-N004 is still reported after the update: $w"

serve_http node1 8443 1
T=$(add_tunnel "$NODE1" 8443)
wait_tunnel_up "$T" 120
pass
