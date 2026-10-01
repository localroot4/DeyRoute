#!/usr/bin/env bash
# S28: a node with an incompatible version: warning, commands are not run, update suggested
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

up hub node1
install_hub hub ir-1 >&2
OLD=/dist/deyroute_${DEY_OLD_VERSION}_linux_${ARCH}.tar.gz
on node1 bash /dist/install.sh --skip-signature --local "$OLD" join "$(join_link)" >&2 || fail "the old node could not join"
NODE1=$(node_id_of node1)
wait_node_online "$NODE1"

info=$(dey node list --json | jq -c --arg id "$NODE1" '.nodes[] | select(.id == $id)')
log "node: $info"
jq -e '.compatible == false' <<<"$info" >/dev/null || fail "the node is not reported incompatible"
w=$(dey status --json | jq -c '[.warnings[]? | select(.code == "DEY-N004")]')
jq -e 'length > 0' <<<"$w" >/dev/null || fail "no DEY-N004 warning in status"
jq -e '.[0].message | test("update"; "i")' <<<"$w" >/dev/null || fail "the warning does not suggest an update: $w"

serve_http node1 8443 1
out=$(dey tunnel add --node "$NODE1" --ports 8443 --ladder "$(it_ladder)" --yes --json 2>&1) && rc=0 || rc=$?
log "tunnel add on the old node: rc=$rc"
units=$(on node1 sh -c "systemctl list-units --all --no-legend 'deyroute-tun@*' | wc -l")
[ "$units" = 0 ] || fail "the incompatible node ran commands ($units tunnel unit(s))"
[ "$rc" != 0 ] || [ "$(dey tunnel list --json | jq -r '.tunnels[0].state')" != UP ] || fail "a tunnel came UP on an incompatible node"
pass
