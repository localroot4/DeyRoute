#!/usr/bin/env bash
# S36: a backend user plants symlinks in the tunnel log dir: the root rotator never writes through them
# The daemons rotate /var/log/deyroute/tunnels as root (copy and truncate above
# 20 MB, checked every minute); the backends run as user deyroute, whose group
# owns that directory (root:deyroute 01770). Control step: a regular tunnel
# log over 20 MB becomes <log>.1.gz and the live file is truncated, while
# systemd keeps feeding StandardOutput=append: files it opens as root. Then
# user deyroute plants links to root files at the names the rotation uses and
# as a *.log of its own, and a directory with files in it at the next shift
# target; after the next rotation the root files are byte-identical, the
# rotation went on and node.log has the warning "tunnel log rotation refused".
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

D=/var/log/deyroute/tunnels
MB22=$((22 << 20)) # above the 20 MB threshold
setup_pair client
serve_http node1 8443 1
T=$(add_tunnel "$NODE1" 8443)
wait_tunnel_up "$T"
# Every unit of a tunnel appends to <tunnel>.log (the drop-in's StandardOutput=).
L=$D/$T.log
on node1 test -f "$L" || fail "no tunnel log $L on node1"

# The directory: root:deyroute, group-writable and sticky, on both sides.
for s in hub node1; do
  m=$(on "$s" stat -c '%a %U:%G' "$D")
  [ "$m" = "1770 root:deyroute" ] || fail "$D on $s is $m, want 1770 root:deyroute"
done
# systemd opened the unit's log as root before it dropped privileges.
[ "$(on node1 stat -c %U "$L")" = root ] || fail "$L is not root's: $(on node1 stat -c '%U:%G %a' "$L")"
# StandardOutput=append: still works for a service of user deyroute with the
# tunnel units' sandbox: systemd opens the file as root, the service writes
# through the descriptor it is given.
on node1 systemd-run --quiet --wait --collect -p User=deyroute -p Group=deyroute -p ProtectSystem=strict \
  -p ReadWritePaths=/var/log/deyroute -p "StandardOutput=append:$D/s36-append.log" /bin/echo s36-append-ok >&2 ||
  fail "a deyroute service with StandardOutput=append: into $D did not run"
[ "$(on node1 cat "$D/s36-append.log")" = s36-append-ok ] || fail "StandardOutput=append: wrote nothing into $D"
[ "$(on node1 stat -c %U "$D/s36-append.log")" = root ] || fail "systemd did not create the log as root"
on node1 rm -f "$D/s36-append.log"

# grow FILE: append 22 MB of lines as root (what a chatty backend writes).
grow() { sh_on node1 "yes '$2' | head -c $MB22 >> '$1'"; }
size() { on node1 stat -c %s "$1"; }

# 1. Control: a regular log over 20 MB is rotated within two ticks.
grow "$L" s36-control-line
rotated_once() { on node1 test -s "$L.1.gz" && [ "$(size "$L")" -lt "$MB22" ]; }
wait_for 150 "$L rotated (two rotation ticks)" rotated_once
on node1 gzip -t "$L.1.gz" || fail "$L.1.gz is not a valid gzip file"
on node1 sh -c "gzip -dc '$L.1.gz' | grep -q s36-control-line" || fail "$L.1.gz does not hold the log"
[ "$(on node1 stat -c '%a %U' "$L.1.gz")" = "600 root" ] || fail "$L.1.gz is $(on node1 stat -c '%a %U' "$L.1.gz")"
m=$(on node1 stat -c '%a %U' /var/lib/deyroute/tmp)
[ "$m" = "700 root" ] || fail "the staging directory /var/lib/deyroute/tmp is $m, want 700 root"
[ -z "$(on node1 find /var/lib/deyroute/tmp -maxdepth 1 -name 'tunnel-log-*')" ] || fail "a staged copy was left behind"
[ "$(fetch_via_hub 8443)" = "$(blob_sha node1)" ] || fail "no traffic through the tunnel after the rotation"
log "control: $L rotated to $L.1.gz and truncated"

# 2. User deyroute plants links to root files.
sh_on node1 "printf 'root file 1\n' > /etc/s36-victim-1 && printf 'root file 2\n' > /etc/s36-victim-2 &&
  yes s36-victim-3 | head -c $MB22 > /etc/s36-victim-3 && chmod 0644 /etc/s36-victim-?"
sums() { on node1 sha256sum /etc/s36-victim-1 /etc/s36-victim-2 /etc/s36-victim-3; }
before=$(sums)
as_backend() { on node1 runuser -u deyroute -- "$@"; }
as_backend ln -s /etc/s36-victim-1 "$L.rotating"
as_backend ln -s /etc/s36-victim-2 "$L.1.gz.tmp"
as_backend ln -s /etc/s36-victim-3 "$D/s36-evil.log"
# A directory with files in it where <log>.1.gz moves next: it must not stop
# the rotation (the rotation removes it, with the link inside, unfollowed).
as_backend mkdir -p "$L.2.gz/sub"
as_backend ln -s /etc/s36-victim-1 "$L.2.gz/sub/link"
# The sticky bit: user deyroute cannot replace root's live log.
if as_backend ln -sf /etc/s36-victim-3 "$L" 2>/dev/null; then fail "user deyroute replaced $L"; fi
on node1 test -f "$L" -a ! -L "$L" || fail "$L is no longer a regular file"

grow "$L" s36-after-plant-line
rotated_twice() { on node1 test -f "$L.2.gz" -a -s "$L.2.gz" && [ "$(size "$L")" -lt "$MB22" ]; }
wait_for 150 "$L rotated again with the links planted (two rotation ticks)" rotated_twice
refused() {
  on node1 cat /var/log/deyroute/node.log |
    jq -rR 'fromjson? | select(.msg == "tunnel log rotation refused") | "\(.file) \(.reason)"'
}
warned() { refused | grep -qx "$D/s36-evil.log symlink" && refused | grep -qx "$L staging"; }
wait_for 150 "node.log warns about the planted links" warned

[ "$(sums)" = "$before" ] || fail "a root file changed: $(sums)"
[ "$(on node1 stat -c %s /etc/s36-victim-3)" = "$MB22" ] || fail "/etc/s36-victim-3 was truncated"
for p in "$L.rotating" "$L.1.gz.tmp"; do
  if on node1 test -e "$p" -o -L "$p"; then fail "the planted $p is still there"; fi
done
on node1 test -L "$D/s36-evil.log" || fail "the planted s36-evil.log link was touched"
if on node1 test -e "$D/s36-evil.log.1.gz"; then fail "the linked s36-evil.log was rotated"; fi
on node1 sh -c "gzip -dc '$L.1.gz' | grep -q s36-after-plant-line" || fail "$L.1.gz does not hold the new lines"
on node1 sh -c "gzip -dc '$L.2.gz' | grep -q s36-control-line" || fail "$L.2.gz does not hold the control lines"
[ -z "$(on node1 find /var/lib/deyroute/tmp -maxdepth 1 -name 'tunnel-log-*')" ] || fail "a staged copy was left behind"
# Once per file and reason while the daemon runs.
dups=$(refused | sort | uniq -d)
[ -z "$dups" ] || fail "warned more than once: $dups"
log "refused: $(refused | tr '\n' ';')"
tunnel_up "$T" || fail "tunnel not UP after the rotations"
pass
