#!/usr/bin/env bash
# S17: connecting to 8.8.8.8:53 through every Forward backend on the node is refused (the node is not an open proxy)
# Online the tunnel's ladder is the Forward transports: xray/reality,
# hysteria2/udp, wireguard/kernel (when the host kernel has WireGuard),
# awg/userspace (when /dev/net/tun exists) and direct/native; offline only
# direct/native. direct/haproxy runs nothing on the node (the hub's HAProxy
# dials the node's own service port), so it has nothing to probe. With each
# transport active:
#  - from outside: open-proxy requests (HTTP CONNECT, SOCKS5/4, GET, raw,
#    DNS) from the client and the hub to every public TCP and UDP listener of
#    the node;
#  - from inside: the hub runs a second client built from the transport's
#    rendered credentials whose destination is 8.8.8.8:53 (xray: a
#    dokodemo-door; hysteria2: tcp/udpForwarding; direct/native: the relay
#    preamble and datagram for target indexes the node does not have;
#    WireGuard and AWG: the hub peer widened to 8.8.8.8/32 and a route into
#    the tunnel), after the same client reached the tunnel's own target.
# The node counts (and drops) what it sends or routes to 8.8.8.8:53: 0.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
serve_http node1 443 1
if [ "$DEY_OFFLINE" = 1 ]; then
  ladder=$(it_ladder)
else
  ladder=xray/reality,hysteria2/udp
  if sh_on hub "ip link add it-wgcheck type wireguard 2>/dev/null && ip link del it-wgcheck"; then
    ladder+=,wireguard/kernel
  else
    log "wireguard/kernel left out: the host kernel has no WireGuard module"
  fi
  if sh_on hub "test -c /dev/net/tun" && sh_on node1 "test -c /dev/net/tun"; then ladder+=,awg/userspace; fi
  ladder+=,direct/native
fi
T=$(add_tunnel "$NODE1" 443 --ladder "$ladder")
wait_tunnel_up "$T" 180
set_failover failback false # stay on each switched-to rung while it is probed

# Count (and drop) anything the node sends or routes to 8.8.8.8:53: a late
# postrouting chain sees what would really leave, after every filter. What
# arrives for 8.8.8.8:53 (routed into a WireGuard tunnel by the hub) is
# counted too, so that probe cannot pass without reaching the node.
on node1 nft -f - <<'EOF'
table inet it_watch {
  counter leak {}
  counter arrived {}
  chain arriving { type filter hook prerouting priority -300; ip daddr 8.8.8.8 th dport 53 counter name arrived; }
  chain leaving { type filter hook postrouting priority 500; ip daddr 8.8.8.8 th dport 53 counter name leak drop; }
}
EOF
watched() { on node1 nft -j list counter inet it_watch "$1" | jq '.nftables[] | select(.counter) | .counter.packets'; }
leaked() { watched leak; }

# From outside: open-proxy style requests to every public TCP and UDP
# listener of the node, from the client and from the hub.
probe_all() {
  local tcp udp p src
  tcp=$(sh_on node1 "ss -Hltn | awk '{print \$4}' | grep -v -e '^127\.' -e '^\[::1\]' | sed 's/.*://' | sort -un")
  udp=$(sh_on node1 "ss -Hlun | awk '{print \$4}' | grep -v -e '^127\.' -e '^\[::1\]' | sed 's/.*://' | sort -un")
  log "public listeners on the node: tcp ${tcp//$'\n'/ } udp ${udp//$'\n'/ }"
  for src in client hub; do
    on "$src" python3 - "$NODE1_IP" "${tcp//$'\n'/,}" "${udp//$'\n'/,}" <<'PY' || true
import socket, sys
host = sys.argv[1]
tcp = [int(p) for p in sys.argv[2].split(",") if p]
udp = [int(p) for p in sys.argv[3].split(",") if p]
dst = bytes([8, 8, 8, 8]) + (53).to_bytes(2, "big")
dns = b"\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00\x07example\x03com\x00\x00\x01\x00\x01"
reqs = [b"CONNECT 8.8.8.8:53 HTTP/1.1\r\nHost: 8.8.8.8:53\r\n\r\n",
        b"\x05\x01\x00" + b"\x05\x01\x00\x01" + dst,
        b"\x04\x01" + (53).to_bytes(2, "big") + bytes([8, 8, 8, 8]) + b"\x00",
        b"GET http://8.8.8.8:53/ HTTP/1.1\r\nHost: 8.8.8.8\r\n\r\n",
        b"8.8.8.8:53\n"]
dgrams = [b"\x00\x00\x00\x01" + dst + dns, dns, b"8.8.8.8:53\n", b"DEYU" + b"\x00" * 26]
for port in tcp:
    for r in reqs:
        try:
            s = socket.create_connection((host, port), timeout=3)
            s.sendall(r)
            s.settimeout(2)
            try:
                s.recv(256)
            except OSError:
                pass
            s.close()
        except OSError:
            pass
for port in udp:
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.settimeout(1)
    for d in dgrams:
        try:
            s.sendto(d, (host, port))
            s.recvfrom(2048)
        except OSError:
            pass
    s.close()
PY
  done
}

# hub_dir TRANSPORT: the hub-side config directory of the transport.
hub_dir() { echo "/etc/deyroute/backends/${1%%/*}/$T/$NODE1/${1#*/}"; }
# try_leak: TCP and UDP DNS queries to the probe client's 127.0.0.1:15353 on
# the hub (its destination is 8.8.8.8:53).
try_leak() {
  on hub python3 - <<'PY' || true
import socket
dns = b"\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00\x07example\x03com\x00\x00\x01\x00\x01"
try:
    s = socket.create_connection(("127.0.0.1", 15353), timeout=3)
    s.sendall(len(dns).to_bytes(2, "big") + dns)
    s.settimeout(3)
    print("tcp reply:", len(s.recv(512)), "bytes")
except OSError as e:
    print("tcp:", e)
u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
u.settimeout(3)
try:
    u.sendto(dns, ("127.0.0.1", 15353))
    print("udp reply:", len(u.recvfrom(512)[0]), "bytes")
except OSError as e:
    print("udp:", e)
PY
}
# probe_client TRANSPORT: the second client of a proxy transport on the hub
# (it-s17-client: 127.0.0.1:15353 -> 8.8.8.8:53, 127.0.0.1:15443 -> the
# tunnel target); it must reach the target before the leak is tried.
probe_client() {
  local tr=$1 dir bin
  dir=$(hub_dir "$tr")
  case $tr in
    xray/*)
      bin=$(sh_on hub "ls -d /var/lib/deyroute/bin/xray/*/xray | head -1")
      on hub python3 - "$dir/config.json" <<'PY'
import json, sys
c = json.load(open(sys.argv[1]))
c["log"] = {"loglevel": "warning"}
c["inbounds"] = [
    {"listen": "127.0.0.1", "port": 15353, "protocol": "dokodemo-door",
     "settings": {"address": "8.8.8.8", "port": 53, "network": "tcp,udp"}},
    {"listen": "127.0.0.1", "port": 15443, "protocol": "dokodemo-door",
     "settings": {"address": "127.0.0.1", "port": 443, "network": "tcp"}}]
json.dump(c, open("/tmp/it-s17-client.json", "w"))
PY
      sh_on hub "systemd-run --quiet --unit it-s17-client $bin run -c /tmp/it-s17-client.json"
      ;;
    hysteria2/*)
      bin=$(sh_on hub "ls -d /var/lib/deyroute/bin/hysteria2/*/hysteria | head -1")
      on hub python3 - "$dir/client.yaml" <<'PY'
import re, sys
c = re.split(r"(?m)^(?:tcp|udp)Forwarding:", open(sys.argv[1]).read())[0]
c += """tcpForwarding:
  - listen: "127.0.0.1:15353"
    remote: "8.8.8.8:53"
  - listen: "127.0.0.1:15443"
    remote: "127.0.0.1:443"
udpForwarding:
  - listen: "127.0.0.1:15353"
    remote: "8.8.8.8:53"
"""
open("/tmp/it-s17-client.yaml", "w").write(c)
PY
      sh_on hub "systemd-run --quiet --unit it-s17-client $bin client -c /tmp/it-s17-client.yaml --disable-update-check"
      ;;
  esac
  wait_for 30 "$tr probe client listening" sh_on hub "ss -Hltn 'sport = :15443' | grep -q ."
  sh_on hub "curl -fsS -o /dev/null --max-time 10 http://127.0.0.1:15443/" ||
    fail "$tr: the probe client built from the hub's credentials does not reach the tunnel target"
  log "$tr: the probe client reaches the tunnel target; trying 8.8.8.8:53 through it"
  try_leak >&2
  sh_on hub "systemctl stop it-s17-client; systemctl reset-failed it-s17-client 2>/dev/null || true"
}
# probe_relay: direct/native with the tunnel token: target index 0 answers,
# indexes the node does not have (TCP preamble, UDP datagram) do not.
probe_relay() {
  on hub python3 - "$(hub_dir direct/native)/relay.json" <<'PY'
import hashlib, hmac, json, os, socket, sys
c = json.load(open(sys.argv[1]))
host, port = c["node"].rsplit(":", 1)
port, key = int(port), c["token"].encode()
dns = b"\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00\x07example\x03com\x00\x00\x01\x00\x01"
def preamble(i):
    m = b"DEYR\x01\x00" + i.to_bytes(2, "big") + os.urandom(8)
    return m + hmac.new(key, m, hashlib.sha256).digest()[:16]
s = socket.create_connection((host, port), timeout=5)
s.sendall(preamble(0) + b"GET / HTTP/1.0\r\n\r\n")
s.settimeout(5)
if not s.recv(12).startswith(b"HTTP/1.0 200"):
    sys.exit("index 0 did not reach the tunnel target")
for i in (len(c["ports"]), 999, 65535):
    try:
        s = socket.create_connection((host, port), timeout=3)
        s.sendall(preamble(i) + len(dns).to_bytes(2, "big") + dns)
        s.settimeout(2)
        s.recv(64)
    except OSError:
        pass
    u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    f = b"DEYU" + i.to_bytes(2, "big") + os.urandom(8) + dns
    u.sendto(f + hmac.new(key, b"\x01" + f, hashlib.sha256).digest()[:16], (host, port))
print("index 0 reached the target; unknown indexes sent")
PY
}
# probe_wg TRANSPORT: the hub's WireGuard peer widened to 8.8.8.8/32 (deyroute
# wg up with a copy of the hub's wg.json) and a route into the tunnel: the
# node must not route it (the firewall confines the tunnel interface even
# though the lab containers have ip_forward=1). The hub's own wg.json is
# applied again afterwards. The arrived counter must grow during this probe:
# an earlier WireGuard probe already raised it.
probe_wg() {
  local tr=$1 cfg iface node_addr a0
  cfg=$(hub_dir "$tr")/wg.json
  iface=$(on hub cat "$cfg" | jq -r .interface)
  node_addr=$(on hub cat "$cfg" | jq -r '.peer.allowed_ips[0]' | cut -d/ -f1)
  sh_on hub "curl -fsS -o /dev/null --max-time 10 http://$node_addr:443/" ||
    fail "$tr: the hub does not reach the target through $iface"
  a0=$(watched arrived)
  on hub cat "$cfg" | jq '.peer.allowed_ips += ["8.8.8.8/32"]' | on hub sh -c 'umask 077 && cat > /tmp/it-s17-wg.json'
  on hub deyroute wg up --config /tmp/it-s17-wg.json >/dev/null || fail "$tr: cannot widen the hub peer"
  log "$tr: hub peer widened to 8.8.8.8/32, routing 8.8.8.8 into $iface"
  sh_on hub "ip route replace 8.8.8.8/32 dev $iface"
  on hub python3 - <<'PY' || true
import socket
dns = b"\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00\x07example\x03com\x00\x00\x01\x00\x01"
socket.socket(socket.AF_INET, socket.SOCK_DGRAM).sendto(dns, ("8.8.8.8", 53))
try:
    socket.create_connection(("8.8.8.8", 53), timeout=3)
except OSError as e:
    print("tcp:", e)
PY
  sh_on hub "ip route del 8.8.8.8/32 dev $iface"
  on hub deyroute wg up --config "$cfg" >/dev/null || fail "$tr: cannot restore the hub peer"
  [ "$(watched arrived)" -gt "$a0" ] || fail "$tr: what the hub routed into $iface never reached the node"
}

rungs=$(tunnel_json "$T" | jq -r '[.rungs[] | select(.node == env.NODE1 and (.skipped | not or . == "")) | .transport] | unique | .[]')
done_list=()
for tr in $rungs; do
  if [ "$(active_transport "$T")" != "$tr" ]; then
    dey tunnel switch "$T" --transport "$tr" --json >/dev/null 2>&1 || fail "cannot switch to $tr"
    is_on() { tunnel_up "$T" && [ "$(active_transport "$T")" = "$tr" ]; }
    wait_for 90 "switch to $tr" is_on
  fi
  log "probing listeners with $tr active"
  probe_all
  case $tr in
    xray/* | hysteria2/*) probe_client "$tr" ;;
    direct/native) probe_relay >&2 ;;
    awg/userspace | wireguard/kernel) probe_wg "$tr" ;;
  esac
  n=$(leaked)
  [ "$n" = 0 ] || fail "with $tr active the node sent or routed $n packet(s) to 8.8.8.8:53"
  done_list+=("$tr")
done
pass "rungs: ${done_list[*]}"
