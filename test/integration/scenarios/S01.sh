#!/usr/bin/env bash
# S01: fresh install on a Tier 1 distribution: < 60s, exit 0, services enabled
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

up hub node1
t0=$SECONDS
install_hub hub ir-1 || fail "the hub installer failed"
th=$((SECONDS - t0))
t0=$SECONDS
install_node node1 || fail "the node installer failed"
tn=$((SECONDS - t0))
[ "$th" -lt 60 ] || fail "hub install took ${th}s (limit 60s)"
[ "$tn" -lt 60 ] || fail "node install took ${tn}s (limit 60s)"

for pair in hub:deyroute-hub node1:deyroute-node; do
  s=${pair%%:*} u=${pair#*:}
  [ "$(on "$s" systemctl is-enabled "$u.service")" = enabled ] || fail "$u is not enabled on $s"
  on "$s" systemctl is-active --quiet "$u.service" || fail "$u is not active on $s"
  [ "$(dey --on "$s" version --json | jq -r .version)" = "$DEY_VERSION" ] || fail "wrong version on $s"
  [ "$(on "$s" readlink -f /usr/local/bin/dey)" = /usr/local/bin/deyroute ] || fail "dey is not a link to deyroute on $s"
  # Permissions (spec 2, 11).
  m=$(on "$s" stat -c '%a %U:%G' /etc/deyroute)
  [ "$m" = "710 root:deyroute" ] || fail "/etc/deyroute on $s is $m, want 710 root:deyroute"
  m=$(on "$s" stat -c '%a' /etc/deyroute/secrets)
  [ "$m" = 700 ] || fail "/etc/deyroute/secrets on $s is $m, want 700"
  bad=$(sh_on "$s" "find /etc/deyroute/secrets -type f ! -perm 0600 -printf '%p %m\n'")
  [ -z "$bad" ] || fail "secret files on $s are not 0600: $bad"
done
wait_node_online "$(node_id_of node1)"
pass "hub ${th}s, node ${tn}s"
