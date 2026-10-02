#!/usr/bin/env bash
# S31: TCP and UDP port maps on backhaul/tcpmux: the UDP companion carries the UDP map, both protocols reach the node
# Not one of the spec's S01-S30: it covers the Backhaul UDP companion
# (docs/backends/backhaul.md) end to end with the real Backhaul binary.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "the Backhaul binary"

setup_pair client
serve_http node1 443 1
# A UDP echo service on node1 127.0.0.1:27015 (the tunnel target).
sh_on node1 "systemd-run --quiet --unit it-udp-echo /usr/bin/python3 -c '
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind((\"127.0.0.1\", 27015))
while True:
    d, a = s.recvfrom(65535)
    s.sendto(b\"echo:\" + d, a)
'"

dey ladder create it-backhaul --rungs backhaul/tcpmux --rungs direct/native >/dev/null
T=$(dey tunnel add --node "$NODE1" --ports 443,27015/udp --ladder it-backhaul --yes --json | jq -r .tunnel.id)
if [ -z "$T" ] || [ "$T" = null ]; then fail "tunnel add failed"; fi
wait_tunnel_up "$T" 180
tr=$(active_transport "$T")
[ "$tr" = backhaul/tcpmux ] || fail "active transport is $tr, want backhaul/tcpmux (skipped: $(tunnel_field "$T" '.skipped'))"

unit=$(unit_of "$T")
sh_on hub "systemctl show -p ExecStart --value $unit" | grep -q ' pair ' || fail "hub unit $unit does not run deyroute pair"
sh_on hub "ss -Hlun 'sport = :27015' | grep -q ." || fail "the hub does not listen on 27015/udp"

got=$(fetch_via_hub 443)
[ "$got" = "$(blob_sha node1)" ] || fail "TCP 443 through the tunnel: wrong data"

udp_roundtrip() {
  sh_on client "/usr/bin/python3 -c '
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(2)
s.sendto(b\"ping\", (\"$HUB_IP\", 27015))
sys.exit(0 if s.recvfrom(65535)[0] == b\"echo:ping\" else 1)
'"
}
wait_for 30 "UDP 27015 echo through the companion" udp_roundtrip

# The pair lives and dies together: killing the companion restarts the unit.
# Only the supervisor's child is killed: its own command line names
# server-udp.toml too, and killing it would restart the unit by itself.
pid=$(main_pid hub "$unit")
sh_on hub "pkill -P $pid -f 'server-udp\.toml'" || fail "no companion process on the hub"
changed() { p=$(main_pid hub "$unit"); [ -n "$p" ] && [ "$p" != 0 ] && [ "$p" != "$pid" ]; }
wait_for 60 "unit $unit restarted after the companion exited" changed
wait_for 60 "UDP 27015 echo after the restart" udp_roundtrip
pass "TCP and UDP through $tr with the UDP companion"
