#!/usr/bin/env bash
# S01: fresh install on a Tier 1 distribution: < 60s, exit 0, services enabled
# Phase 2 "join < 30s" is timed here too: from the start of the node's
# one-line `install.sh … join` to the node being online on the hub.
# A fresh hub never updates itself: the automatic update is off, config.yaml
# has no hub.auto_update, and there is no migration notice (DR-05).
# The hub is set up the way automation does it (DR-07): installed with
# --no-setup, then `setup --public-ip` stores exactly the given address (the
# lab address is private, hence --allow-private-ip).
# Both daemons run sandboxed (DR-20): ProtectSystem=strict, so /usr is
# read-only for them while /var/lib/deyroute stays writable, and the bounding
# set holds CAP_NET_ADMIN but neither CAP_SYS_ADMIN nor CAP_SYS_MODULE.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

up hub node1
t0=$SECONDS
on hub bash /dist/install.sh --local "$ARCHIVE" --no-setup || fail "the hub installer failed"
sj=$(dey setup --role hub --name ir-1 --public-ip "$HUB_IP" --allow-private-ip --yes --json) || fail "hub setup failed: $sj"
th=$((SECONDS - t0))
jq -e --arg ip "$HUB_IP" '.public_ip == $ip and .public_ip_source == "flag"' <<<"$sj" >/dev/null ||
  fail "setup --public-ip: $sj"
on hub grep -qx "  public_ip: $HUB_IP" /etc/deyroute/config.yaml || fail "config.yaml does not have hub.public_ip $HUB_IP"
link=$(join_link)
j0=$(date +%s%3N)
t0=$SECONDS
install_node node1 "$link" || fail "the node installer failed"
tn=$((SECONDS - t0))
[ "$th" -lt 60 ] || fail "hub install took ${th}s (limit 60s)"
[ "$tn" -lt 60 ] || fail "node install took ${tn}s (limit 60s)"
NODE1=$(node_id_of node1)
wait_node_online "$NODE1" 30
jms=$(($(date +%s%3N) - j0))
log "join: node online ${jms}ms after the installer started"
[ "$jms" -lt 30000 ] || fail "join took ${jms}ms (limit 30s)"

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
  # Sandbox (DR-20), seen from inside the daemon's mount namespace.
  [ "$(on "$s" systemctl show -p ProtectSystem --value "$u.service")" = strict ] || fail "$u on $s: ProtectSystem is not strict"
  pid=$(main_pid "$s" "$u.service")
  if on "$s" nsenter -t "$pid" -m touch /usr/.dey-rw-probe 2>/dev/null; then
    on "$s" rm -f /usr/.dey-rw-probe
    fail "$u on $s can write /usr"
  fi
  on "$s" nsenter -t "$pid" -m sh -c 'touch /var/lib/deyroute/.dey-rw-probe && rm /var/lib/deyroute/.dey-rw-probe' ||
    fail "$u on $s cannot write /var/lib/deyroute"
  bnd=$((16#$(on "$s" sed -n 's/^CapBnd:[[:space:]]*//p' "/proc/$pid/status")))
  # Bits: CAP_NET_ADMIN 12, CAP_SYS_MODULE 16, CAP_SYS_ADMIN 21.
  if [ $((bnd >> 12 & 1)) != 1 ] || [ $((bnd >> 16 & 1)) != 0 ] || [ $((bnd >> 21 & 1)) != 0 ]; then
    fail "$u on $s: unexpected capability bounding set $(printf '%x' "$bnd")"
  fi
done

au=$(dey update auto --json)
jq -e '.enabled == false and .source == "config"' <<<"$au" >/dev/null || fail "update auto on a fresh hub: $au"
if on hub grep -q auto_update /etc/deyroute/config.yaml; then fail "a fresh config.yaml sets hub.auto_update"; fi
if has_event auto_update_default_off; then fail "a fresh install recorded auto_update_default_off"; fi
dey status --json | jq -e '[.warnings[]?.message | select(test("automatic update is now off"))] | length == 0' >/dev/null ||
  fail "a fresh install shows the automatic update notice"
pass "hub ${th}s, node ${tn}s, join ${jms}ms"
