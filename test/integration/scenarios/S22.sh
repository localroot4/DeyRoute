#!/usr/bin/env bash
# S22: backup -> restore on a new hub: the nodes reconnect without joining again
# The automatic update switch (hub.auto_update in config.yaml) travels with
# the backup; while it is set a rollback is refused with DEY-S011 (DR-05).
# DR-08: the lab addresses are private, so a restore without an address is
# DEY-I025 and restores nothing; with --public-ip the restored hub takes that
# address and a node-dials-hub (reverse) backend dials it.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
serve_http node1 8443 1
T=$(add_tunnel "$NODE1" 8443)
wait_tunnel_up "$T"
up hub2
on hub2 bash /dist/install.sh --local "$ARCHIVE" --no-setup >&2

# Move: tell the nodes the new address, take the backup, retire the old hub.
ann=$(dey hub announce-move "$HUB2_IP:$CONTROL_PORT" --json)
jq -e --arg n "$NODE1" '.accepted | index($n) != null' <<<"$ann" >/dev/null || fail "announce-move: $ann"
au=$(dey update auto on --json)
jq -e '.enabled == true and .source == "config"' <<<"$au" >/dev/null || fail "update auto on: $au"
on hub grep -q 'auto_update: true' /etc/deyroute/config.yaml || fail "update auto on did not write hub.auto_update"
out=$(expect_code DEY-S011 dey update --rollback --yes)
grep -q 'hub.auto_update' <<<"$out" || fail "DEY-S011 does not name hub.auto_update: $out"
path=$(sh_on hub "DEYROUTE_BACKUP_PASSPHRASE=it-pass deyroute backup --json" | jq -r .path)
{ [ -n "$path" ] && [ "$path" != null ]; } || fail "backup wrote no file"
tmp=$(mktemp -d)
docker cp "$(cid hub):$path" "$tmp/backup" >/dev/null
docker cp "$tmp/backup" "$(cid hub2):/root/deyroute-backup.tar.gz.age" >/dev/null
rm -rf "$tmp"
docker stop "$(cid hub)" >/dev/null

out=$(expect_code DEY-I025 sh_on hub2 "DEYROUTE_BACKUP_PASSPHRASE=it-pass deyroute restore /root/deyroute-backup.tar.gz.age --yes")
grep -q -- '--public-ip' <<<"$out" || fail "DEY-I025 does not name --public-ip: $out"
on hub2 test ! -e /etc/deyroute/config.yaml || fail "DEY-I025 left a config.yaml on hub2"
res=$(sh_on hub2 "DEYROUTE_BACKUP_PASSPHRASE=it-pass deyroute restore /root/deyroute-backup.tar.gz.age --public-ip $HUB2_IP --yes --json") ||
  { echo "$res" >&2; fail "restore failed"; }
log "restore: $(jq -c '{role, hub_name, public_ip, address_source, address_changed, warnings, service_started}' <<<"$res")"
jq -e --arg ip "$HUB2_IP" '.public_ip == $ip and .address_source == "flag" and .address_changed == true
  and (.warnings | map(test("not a public IP")) | any)' <<<"$res" >/dev/null || fail "restore --public-ip: $res"
on hub2 grep -q "public_ip: $HUB2_IP" /etc/deyroute/config.yaml || fail "hub.public_ip of the restored hub is not $HUB2_IP"
online2() { dey --on hub2 node list --json | jq -e --arg id "$NODE1" '.nodes[] | select(.id == $id) | .online' >/dev/null; }
wait_for 120 "node online on the new hub" online2
up2() { [ "$(dey --on hub2 tunnel show "$T" --json | jq -r .state)" = UP ]; }
wait_for 120 "tunnel UP on the new hub" up2
[ "$(fetch_via_hub 8443 "$HUB2_IP")" = "$(blob_sha node1)" ] || fail "no traffic through the new hub"
[ "$(dey --on node1 status --json | jq -r .node.hub_addr)" = "$HUB2_IP:$CONTROL_PORT" ] || fail "node hub_addr not updated"
# A reverse backend: the node dials hub.public_ip, which must be the new one.
if [ "$DEY_OFFLINE" != 1 ]; then
  dey --on hub2 tunnel switch "$T" --transport backhaul/tcpmux --json >/dev/null
  on_bh2() { [ "$(dey --on hub2 tunnel show "$T" --json | jq -r '.state + " " + .active_transport')" = "UP backhaul/tcpmux" ]; }
  wait_for 120 "tunnel on backhaul/tcpmux through the new hub" on_bh2
  bh=/etc/deyroute/backends/backhaul/$T
  sh_on node1 "grep -rqsF -- '$HUB2_IP:' $bh" || fail "the node's backhaul config does not dial the new hub address $HUB2_IP"
  if sh_on node1 "grep -rqsF -- '$HUB_IP:' $bh"; then fail "the node's backhaul config still dials the old hub address $HUB_IP"; fi
  [ "$(fetch_via_hub 8443 "$HUB2_IP")" = "$(blob_sha node1)" ] || fail "no traffic through the new hub on backhaul/tcpmux"
else
  log "offline: only direct/native runs (the hub dials the node); the reverse-backend check needs downloaded backends"
fi
au=$(dey --on hub2 update auto --json)
jq -e '.enabled == true' <<<"$au" >/dev/null || fail "the restored hub lost hub.auto_update: $au"
pass
