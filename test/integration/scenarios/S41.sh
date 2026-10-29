#!/usr/bin/env bash
# S41: hub serves a tampered deyroute binary with a correct announced sha256: the node refuses it (DEY-S001)
# The hub updates itself from the signed lab release (release_mirror) and
# keeps that release for its nodes under /var/lib/deyroute/bin/<version>/.
# Then the binary inside the kept archive is replaced, as by someone who took
# over the hub: the hub announces the sha256 of the archive it serves, which
# matches what it serves, but not the signed SHA256SUMS. The node checks the
# release with the key built into it, refuses it with DEY-S001 and keeps
# running its version (DR-03).
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

up hub node1 client
OLD=/dist/deyroute_${DEY_OLD_VERSION}_linux_${ARCH}.tar.gz
on hub bash /dist/install.sh --local "$OLD" --role hub --name ir-1 --yes >&2
on node1 bash /dist/install.sh --local "$OLD" join "$(join_link)" >&2
NODE1=$(node_id_of node1)
wait_node_online "$NODE1"
release_mirror
node_bin=$(on node1 sha256sum /usr/local/bin/deyroute)

# The node stays away while the hub updates, so that the hub's update of the
# node (self.update when it connects) comes only after the tampering.
on node1 systemctl stop deyroute-node
upd=$(dey update --version "$DEY_VERSION" --yes --json) || { echo "$upd" >&2; fail "deyroute update on the hub failed"; }
hub_on_new() { [ "$(dey status --json | jq -r .version)" = "$DEY_VERSION" ]; }
wait_for 90 "the hub runs $DEY_VERSION" hub_on_new

K=/var/lib/deyroute/bin/$DEY_VERSION A=deyroute_${DEY_VERSION}_linux_$ARCH.tar.gz
on hub test -f "$K/$A" -a -f "$K/SHA256SUMS" -a -f "$K/SHA256SUMS.minisig" || fail "the hub kept no signed release in $K"
# Another binary in the archive: the release binary with bytes appended
# (it would still run), packed as the release packs it.
on hub bash -s "$K/$A" <<'SH' || fail "the archive kept by the hub could not be changed"
set -euo pipefail
a=$1 t=$(mktemp -d)
tar -C "$t" -xzf "$a"
mapfile -t bins < <(find "$t" -type f -name deyroute)
[ "${#bins[@]}" = 1 ] || { echo "want one deyroute in $a, found ${#bins[@]}" >&2; exit 1; }
printf 'S41: not the signed binary' >>"${bins[0]}"
(cd "$t" && tar -czf "$a.new" -- *)
mv -f "$a.new" "$a"
rm -rf "$t"
SH
served=$(on hub sha256sum "$K/$A" | cut -d' ' -f1)
signed=$(on hub cat "$K/SHA256SUMS" | awk -v f="$A" '$2 == f || $2 == "*" f { print $1 }')
{ [ -n "$signed" ] && [ "$served" != "$signed" ]; } || fail "the archive was not changed against the signed SHA256SUMS ($served, $signed)"
log "served archive sha256 $served, signed $signed"

n=$(sh_on hub "cat /var/log/deyroute/hub.log | wc -l")
on node1 systemctl start deyroute-node
M="node could not install the hub's version; it is tried again when it reconnects"
refused() {
  sh_on hub "tail -n +$((n + 1)) /var/log/deyroute/hub.log" | jq -e -R --arg m "$M" --arg node "$NODE1" 'fromjson? |
    select(.msg == $m and .node == $node and .code == "DEY-S001")' >/dev/null
}
wait_for 120 "the hub logs the node's refusal (DEY-S001)" refused
sh_on hub "tail -n +$((n + 1)) /var/log/deyroute/hub.log" | grep -F "$M" >&2 || true
# The node reports the refusal of the archive in its heartbeat (last_error).
node_info() { dey node list --json | jq -c --arg id "$NODE1" '.nodes[] | select(.id == $id)'; }
reports_s001() { node_info | jq -e --arg a "$A" '(.last_error // "") | test("DEY-S001") and contains($a)' >/dev/null; }
wait_for 30 "the node reports DEY-S001 for $A" reports_s001
info=$(node_info)
log "node: $info"
jq -e --arg v "$DEY_OLD_VERSION" '.online and .version == $v' <<<"$info" >/dev/null || fail "the node does not run $DEY_OLD_VERSION any more: $info"
[ "$(on node1 sha256sum /usr/local/bin/deyroute)" = "$node_bin" ] || fail "the node binary changed"
[ "$(dey --on node1 version --json | jq -r .version)" = "$DEY_OLD_VERSION" ] || fail "the node binary is not $DEY_OLD_VERSION"
on node1 systemctl is-active --quiet deyroute-node || fail "deyroute-node is not running"
on node1 cat /var/log/deyroute/node.log | jq -e -R 'fromjson? |
  select(.msg == "command failed" and .command == "self.update" and .code == "DEY-S001")' >/dev/null ||
  fail "the node logged no refused self.update (DEY-S001)"
if on node1 cat /var/log/deyroute/node.log | jq -e -R 'fromjson? | select(.msg | test("signed release installed"))' >/dev/null; then
  fail "the node installed the tampered release"
fi
pass
