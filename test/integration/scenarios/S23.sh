#!/usr/bin/env bash
# S23: uninstall restores the system: nft ruleset, sysctl and files as before the install
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

up hub node1 client
snapshot() {
  sh_on "$1" "echo '## nft'; nft list ruleset
    echo '## sysctl'; sysctl -a 2>/dev/null | grep -E '^net\.(core|ipv4|ipv6)\.|^vm\.|^fs\.' |
      grep -v -E 'conf\.(eth|veth|all|default)[^ ]*\.stable_secret|nf_conntrack_count|tcp_fastopen_key|\.random|dirty_|nr_|inode-|dentry-|file-nr|drop_caches|stat_refresh|min_free|flow_limit|netdev_rss_key|entropy'
    echo '## files'; find /etc /usr/local /var/lib /var/log /opt /srv /root -xdev \
      \( -path /var/lib/systemd -o -path /var/log/journal -o -path /etc/ld.so.cache -o -path /var/lib/apt \
         -o -path /var/cache -o -path /var/lib/dpkg -o -path /root/.cache -o -path /etc/machine-id \
         -o -path /var/lib/dbus -o -path /var/log/btmp -o -path /var/log/wtmp -o -path /var/log/lastlog \
         -o -path /srv/it -o -path /etc/systemd/system/it-http-8443.service \
         -o -path /etc/systemd/system/multi-user.target.wants/it-http-8443.service \) -prune -o -print | sort
    echo '## accounts'; getent passwd deyroute; getent group deyroute; true"
}
serve_http node1 8443 1 # the lab service exists before and after
before_h=$(snapshot hub)
before_n=$(snapshot node1)

install_hub hub ir-1 >&2
install_node node1 >&2
NODE1=$(node_id_of node1)
wait_node_online "$NODE1"
dey optimize apply --profile balanced --json >/dev/null
T=$(add_tunnel "$NODE1" 8443)
wait_tunnel_up "$T"

res=$(dey uninstall --nodes --yes --json) || { echo "$res" >&2; fail "uninstall failed"; }
jq -e --arg n "$NODE1" '.nodes | index($n) != null' <<<"$res" >/dev/null || fail "the node was not uninstalled first: $res"
node_gone() { on node1 test ! -e /usr/local/bin/deyroute; }
wait_for 60 "node uninstalled" node_gone
sleep 3

rc=0
diff <(echo "$before_h") <(snapshot hub) >&2 || { log "hub differs from before the install (above)"; rc=1; }
diff <(echo "$before_n") <(snapshot node1) >&2 || { log "node differs from before the install (above)"; rc=1; }
[ "$rc" = 0 ] || fail "uninstall left changes behind"
pass
