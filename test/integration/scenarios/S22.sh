#!/usr/bin/env bash
# S22: backup -> restore on a new hub: the nodes reconnect without joining again
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
serve_http node1 8443 1
T=$(add_tunnel "$NODE1" 8443)
wait_tunnel_up "$T"
up hub2
on hub2 bash /dist/install.sh --skip-signature --local "$ARCHIVE" --no-setup >&2

# Move: tell the nodes the new address, take the backup, retire the old hub.
ann=$(dey hub announce-move "$HUB2_IP:$CONTROL_PORT" --json)
jq -e --arg n "$NODE1" '.accepted | index($n) != null' <<<"$ann" >/dev/null || fail "announce-move: $ann"
path=$(sh_on hub "DEYROUTE_BACKUP_PASSPHRASE=it-pass deyroute backup --json" | jq -r .path)
{ [ -n "$path" ] && [ "$path" != null ]; } || fail "backup wrote no file"
tmp=$(mktemp -d)
docker cp "$(cid hub):$path" "$tmp/backup" >/dev/null
docker cp "$tmp/backup" "$(cid hub2):/root/deyroute-backup.tar.gz.age" >/dev/null
rm -rf "$tmp"
docker stop "$(cid hub)" >/dev/null

res=$(sh_on hub2 "DEYROUTE_BACKUP_PASSPHRASE=it-pass deyroute restore /root/deyroute-backup.tar.gz.age --yes --json") ||
  { echo "$res" >&2; fail "restore failed"; }
log "restore: $(jq -c '{role, hub_name, public_ip, address_changed, service_started}' <<<"$res")"
online2() { dey --on hub2 node list --json | jq -e --arg id "$NODE1" '.nodes[] | select(.id == $id) | .online' >/dev/null; }
wait_for 120 "node online on the new hub" online2
up2() { [ "$(dey --on hub2 tunnel show "$T" --json | jq -r .state)" = UP ]; }
wait_for 120 "tunnel UP on the new hub" up2
[ "$(fetch_via_hub 8443 "$HUB2_IP")" = "$(blob_sha node1)" ] || fail "no traffic through the new hub"
[ "$(dey --on node1 status --json | jq -r .node.hub_addr)" = "$HUB2_IP:$CONTROL_PORT" ] || fail "node hub_addr not updated"
pass
