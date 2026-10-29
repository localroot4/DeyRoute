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
# Then user deyroute plants a link to a root file at the log name of the next
# tunnel on both sides before the tunnel is added: the services replace it
# with root's own regular file before systemd opens it as root, so the root
# file stays byte-identical, the tunnel comes UP with its output in its log
# and hub.log and node.log have the warning "tunnel log replaced".
# Then an existing 0770 directory gets the mode from the installer's repair
# run, and from a restart of the services alone (what deyroute update does);
# a link user deyroute put at the log name of the running tunnel while the
# directory was 0770 is replaced when the services start, so systemd's own
# restart of a killed backend appends to root's file. A chain of directories
# user deyroute builds at the next tunnel's log name is moved aside at once
# (the tunnel comes UP) and deleted by the rotation loop in the background.
# Last, a file with a second hard link at a log name is not read by
# deyroute logs (DEY-X022).
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

# 3. User deyroute plants a link to a root file at the log name of the next
# tunnel, on the hub and on node1, before the tunnel exists. systemd opens a
# unit's StandardOutput=append: file as root and follows a link; the services
# replace the link with root's own regular file before the units start.
T2=s36-next
L2=$D/$T2.log
serve_http node1 8444 1
for s in hub node1; do
  sh_on "$s" "printf 'root file 4\n' > /etc/s36-victim-4 && chmod 0644 /etc/s36-victim-4"
  on "$s" runuser -u deyroute -- ln -s /etc/s36-victim-4 "$L2" || fail "user deyroute could not add a name to $D on $s"
done
sum4() { on "$1" sha256sum /etc/s36-victim-4; }
hub4=$(sum4 hub) node4=$(sum4 node1)
id=$(add_tunnel "$NODE1" 8444 --name "$T2")
[ "$id" = "$T2" ] || fail "the new tunnel is $id, want $T2"
wait_tunnel_up "$T2"
[ "$(sum4 hub)" = "$hub4" ] || fail "/etc/s36-victim-4 changed on the hub"
[ "$(sum4 node1)" = "$node4" ] || fail "/etc/s36-victim-4 changed on node1"
replaced() { # replaced SERVER LOG: "<file> <reason>" of every "tunnel log replaced"
  on "$1" cat "/var/log/deyroute/$2.log" |
    jq -rR 'fromjson? | select(.msg == "tunnel log replaced") | "\(.file) \(.reason)"'
}
for s in hub node1; do
  on "$s" test -f "$L2" -a ! -L "$L2" || fail "$L2 on $s is not a regular file"
  m=$(on "$s" stat -c '%U %h' "$L2")
  [ "$m" = "root 1" ] || fail "$L2 on $s is $m, want root's with one link"
  wait_for 30 "$L2 on $s holds the tunnel's output" on "$s" test -s "$L2"
done
replaced hub hub | grep -qx "$L2 symlink" || fail "hub.log has no 'tunnel log replaced' for $L2: $(replaced hub hub)"
replaced node1 node | grep -qx "$L2 symlink" || fail "node.log has no 'tunnel log replaced' for $L2: $(replaced node1 node)"
[ "$(fetch_via_hub 8444)" = "$(blob_sha node1)" ] || fail "no traffic through $T2"
log "replaced: hub $(replaced hub hub | tr '\n' ';') node1 $(replaced node1 node | tr '\n' ';')"

# 4. An existing directory of an older install (0770, no sticky bit) gets the
# mode from the installer's repair run (install -d on an existing directory).
on node1 chmod 0770 "$D"
on node1 bash /dist/install.sh --local "$ARCHIVE" >&2 || fail "the installer's repair run failed"
m=$(on node1 stat -c '%a %U:%G' "$D")
[ "$m" = "1770 root:deyroute" ] || fail "after the installer's repair run $D is $m, want 1770 root:deyroute"
wait_node_online "$NODE1" 90
wait_tunnel_up "$T"

# 5. ... and from a restart of the services alone, without the installer
# (deyroute update restarts them): they add the sticky bit when they start.
# In the 0770 directory user deyroute can remove root's log of the running
# tunnel and put a link there, which systemd would follow as root on the
# unit's next restart of its own (Restart=always): the services replace it
# when they start, before they adopt the running units.
on hub chmod 0770 "$D"
on node1 chmod 0770 "$D"
for s in hub node1; do
  sh_on "$s" "printf 'root file 5\n' > /etc/s36-victim-5 && chmod 0644 /etc/s36-victim-5"
  on "$s" runuser -u deyroute -- sh -c "rm -f '$L' && ln -s /etc/s36-victim-5 '$L'" ||
    fail "user deyroute could not replace $L in the 0770 directory on $s"
done
sum5() { on "$1" sha256sum /etc/s36-victim-5; }
hub5=$(sum5 hub) node5=$(sum5 node1)
on hub systemctl restart deyroute-hub
on node1 systemctl restart deyroute-node
sticky() { [ "$(on "$1" stat -c '%a %U:%G' "$D")" = "1770 root:deyroute" ]; }
for s in hub node1; do
  wait_for 30 "$D on $s back to 1770 root:deyroute after the service restart" sticky "$s"
done
made_sticky() {
  on "$1" cat "/var/log/deyroute/$2.log" |
    jq -rR 'fromjson? | select(.msg == "tunnel log directory made sticky") | "\(.from) \(.to)"' | grep -qx '0770 1770'
}
made_sticky hub hub || fail "hub.log does not say the tunnel log directory was made sticky"
made_sticky node1 node || fail "node.log does not say the tunnel log directory was made sticky"
own_log() { on "$1" test -f "$L" -a ! -L "$L" && [ "$(on "$1" stat -c '%U %h' "$L")" = "root 1" ]; }
for s in hub node1; do
  wait_for 30 "$L on $s is root's regular file again after the service restart" own_log "$s"
done
[ "$(sum5 hub)" = "$hub5" ] || fail "/etc/s36-victim-5 changed on the hub"
[ "$(sum5 node1)" = "$node5" ] || fail "/etc/s36-victim-5 changed on node1"
replaced hub hub | grep -qx "$L symlink" || fail "hub.log has no 'tunnel log replaced' for $L: $(replaced hub hub)"
replaced node1 node | grep -qx "$L symlink" || fail "node.log has no 'tunnel log replaced' for $L: $(replaced node1 node)"
wait_node_online "$NODE1" 90
wait_tunnel_up "$T"
# The running backends still write to what systemd opened before. When one
# exits, systemd restarts it by itself (Restart=always, 2 s later) and opens
# $L again as root, without the services: it finds root's file now.
unit=$(unit_of "$T")
{ [ -n "$unit" ] && [ "$unit" != null ]; } || fail "no active unit for $T"
restarted() { p=$(main_pid "$1" "$unit"); [ "${p:-0}" -gt 0 ] && [ "$p" != "$2" ]; }
for s in hub node1; do
  pid=$(main_pid "$s" "$unit")
  [ "${pid:-0}" -gt 0 ] || fail "no main pid for $unit on $s"
  on "$s" kill -9 "$pid"
  wait_for 30 "$unit restarted by systemd on $s" restarted "$s" "$pid"
  own_log "$s" || fail "$L on $s is not root's regular file after systemd restarted $unit"
done
[ "$(sum5 hub)" = "$hub5" ] || fail "/etc/s36-victim-5 changed on the hub after systemd restarted $unit"
[ "$(sum5 node1)" = "$node5" ] || fail "/etc/s36-victim-5 changed on node1 after systemd restarted $unit"
wait_tunnel_up "$T"
wait_tunnel_up "$T2"

# 6. User deyroute builds a chain of directories at the log name of the next
# tunnel on both sides. The services rename it aside at once instead of
# walking it (the start does not wait for it) and the rotation loop deletes
# it in the background, a bounded amount every minute.
T3=s36-deep
L3=$D/$T3.log
serve_http node1 8445 1
for s in hub node1; do
  sh_on "$s" "printf 'root file 6\n' > /etc/s36-victim-6 && chmod 0644 /etc/s36-victim-6"
  on "$s" runuser -u deyroute -- python3 -c 'import os, sys
os.mkdir(sys.argv[1])
os.chdir(sys.argv[1])
for _ in range(3000):
    os.mkdir("d")
    os.chdir("d")
os.symlink("/etc/s36-victim-6", "link")' "$L3" || fail "user deyroute could not build a directory chain at $L3 on $s"
done
sum6() { on "$1" sha256sum /etc/s36-victim-6; }
hub6=$(sum6 hub) node6=$(sum6 node1)
id=$(add_tunnel "$NODE1" 8445 --name "$T3")
[ "$id" = "$T3" ] || fail "the new tunnel is $id, want $T3"
wait_tunnel_up "$T3"
for s in hub node1; do
  on "$s" test -f "$L3" -a ! -L "$L3" || fail "$L3 on $s is not a regular file"
  m=$(on "$s" stat -c '%U %h' "$L3")
  [ "$m" = "root 1" ] || fail "$L3 on $s is $m, want root's with one link"
done
replaced hub hub | grep -qx "$L3 not_regular" || fail "hub.log has no 'tunnel log replaced' for $L3: $(replaced hub hub)"
replaced node1 node | grep -qx "$L3 not_regular" || fail "node.log has no 'tunnel log replaced' for $L3: $(replaced node1 node)"
[ "$(fetch_via_hub 8445)" = "$(blob_sha node1)" ] || fail "no traffic through $T3"
aside_gone() { [ -z "$(on "$1" find "$D" -maxdepth 1 -name "$T3.log.removed-*")" ]; }
for s in hub node1; do
  wait_for 200 "the chain moved aside on $s deleted by the rotation loop" aside_gone "$s"
done
[ "$(sum6 hub)" = "$hub6" ] || fail "/etc/s36-victim-6 changed on the hub"
[ "$(sum6 node1)" = "$node6" ] || fail "/etc/s36-victim-6 changed on node1"
log "chain at $L3 moved aside and deleted on both sides"

# 7. A file with a second hard link at a log name is not read: the node
# answers deyroute logs (and its hub's logs.tail) with DEY-X022. With
# fs.protected_hardlinks=1 (the default) user deyroute can link only its
# own files; with 0 it could link /etc/shadow, which the node reads as root.
# (Both names in $D: /tmp may be another file system.)
on node1 runuser -u deyroute -- sh -c "printf 's36-hardlink-secret\n' > '$D/s36-hl.src' && ln '$D/s36-hl.src' '$D/s36-hl.log'" ||
  fail "user deyroute could not hard-link a file in $D"
out=$(expect_code DEY-X022 dey --on node1 logs s36-hl)
if grep -q s36-hardlink-secret <<<"$out"; then fail "deyroute logs read a file with a second hard link: $out"; fi
on node1 rm -f "$D/s36-hl.log" "$D/s36-hl.src"
pass
