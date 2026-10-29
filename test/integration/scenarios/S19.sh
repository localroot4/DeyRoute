#!/usr/bin/env bash
# S19: updating deyroute loses no traffic on the active tunnel
# The update replaces the binary and restarts deyroute-hub / deyroute-node while the
# backend units keep running. The hub updates itself with `deyroute update
# --version` from a lab HTTPS mirror that serves the signed lab release
# (release_mirror); the node follows through self.update alone, no installer
# runs on it: it installs the signed release the hub kept for it only after
# checking it with the key built into it (DR-03). Hub and node run the same
# architecture, so this is the hub's own-architecture path.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

up hub node1 client
OLD=/dist/deyroute_${DEY_OLD_VERSION}_linux_${ARCH}.tar.gz
on hub bash /dist/install.sh --local "$OLD" --role hub --name ir-1 --yes >&2
on node1 bash /dist/install.sh --local "$OLD" join "$(join_link)" >&2
NODE1=$(node_id_of node1)
wait_node_online "$NODE1"
release_mirror
vpn_up node1 8443 1
T=$(add_tunnel "$NODE1" 8443)
wait_tunnel_up "$T"
unit=$(unit_of "$T")
hp=$(main_pid hub "$unit") np=$(main_pid node1 "$unit")
node_bin=$(on node1 sha256sum /usr/local/bin/deyroute)

start_prober 8443
sleep 3
upd=$(dey update --version "$DEY_VERSION" --yes --json) || { echo "$upd" >&2; fail "deyroute update on the hub failed"; }
log "update: $(jq -c '.update | {current, previous}' <<<"$upd")"
hub_on_new() { [ "$(dey status --json | jq -r .version)" = "$DEY_VERSION" ]; }
wait_for 90 "the hub runs $DEY_VERSION" hub_on_new
node_on_new() {
  dey node list --json | jq -e --arg id "$NODE1" --arg v "$DEY_VERSION" \
    '.nodes[] | select(.id == $id) | .online and .version == $v' >/dev/null
}
wait_for 120 "the node follows the hub to $DEY_VERSION" node_on_new
sleep 5
out=$(stop_prober)
total=$(on client sh -c 'wc -l < /tmp/prober.log')
fails=$(on client grep -c ' fail$' /tmp/prober.log || true)
log "requests: $total, failed: ${fails:-0}, longest outage ${out}ms"
[ "$(dey version --json | jq -r .version)" = "$DEY_VERSION" ] || fail "hub not updated"
[ "$(dey --on node1 version --json | jq -r .version)" = "$DEY_VERSION" ] || fail "node not updated"
[ "$(on node1 sha256sum /usr/local/bin/deyroute)" != "$node_bin" ] || fail "the node binary did not change"
# The hub kept the signed release it served, and the node installed it
# through self.update after checking it.
for f in SHA256SUMS SHA256SUMS.minisig "deyroute_${DEY_VERSION}_linux_$ARCH.tar.gz"; do
  on hub cmp -s "/dist/$f" "/var/lib/deyroute/bin/$DEY_VERSION/$f" || fail "the hub did not keep $f of the release it serves"
done
signed=$(on node1 cat /var/log/deyroute/node.log | jq -rR 'fromjson? |
  select(.msg == "deyroute binary of the signed release installed; restarting the node service") | .version')
grep -qx "$DEY_VERSION" <<<"$signed" || fail "the node did not install the signed release through self.update"
if on hub grep -q "node could not install the hub's version" /var/log/deyroute/hub.log; then
  fail "a self.update of the node failed: $(on hub grep "node could not install the hub's version" /var/log/deyroute/hub.log)"
fi
[ "${fails:-0}" = 0 ] || fail "$fails request(s) failed during the update"
[ "$(main_pid hub "$unit")" = "$hp" ] || fail "the hub backend unit was restarted by the update"
[ "$(main_pid node1 "$unit")" = "$np" ] || fail "the node backend unit was restarted by the update"
tunnel_up "$T" || fail "tunnel not UP after the update"
pass "$total requests, 0 failed"
