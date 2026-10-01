#!/usr/bin/env bash
# S17: connecting to 8.8.8.8:53 through every Forward backend on the node is refused (the node is not an open proxy)
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
serve_http node1 443 1
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T" 180

# Count (and drop) anything the node sends to 8.8.8.8:53.
on node1 nft -f - <<'EOF'
table inet it_watch {
  counter leak {}
  chain out {
    type filter hook output priority -60;
    ip daddr 8.8.8.8 th dport 53 counter name leak drop
  }
}
EOF

# Open-proxy style requests (HTTP CONNECT, SOCKS5, SOCKS4, raw) from the client
# and from the hub to every public TCP listener of a tunnel unit on the node.
probe_all() {
  local ports p src
  ports=$(sh_on node1 "ss -Hltn | awk '{print \$4}' | grep -v -e '^127\.' -e '^\[::1\]' | sed 's/.*://' | sort -un")
  for p in $ports; do
    for src in client hub; do
      on "$src" python3 - "$NODE1_IP" "$p" <<'PY' || true
import socket, sys
host, port = sys.argv[1], int(sys.argv[2])
reqs = [b"CONNECT 8.8.8.8:53 HTTP/1.1\r\nHost: 8.8.8.8:53\r\n\r\n",
        b"\x05\x01\x00" + b"\x05\x01\x00\x01" + bytes([8, 8, 8, 8]) + (53).to_bytes(2, "big"),
        b"\x04\x01" + (53).to_bytes(2, "big") + bytes([8, 8, 8, 8]) + b"\x00",
        b"GET http://8.8.8.8:53/ HTTP/1.1\r\nHost: 8.8.8.8\r\n\r\n",
        b"8.8.8.8:53\n"]
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
PY
    done
  done
}

rungs=$(tunnel_json "$T" | jq -r '[.rungs[] | select(.node == env.NODE1 and (.skipped | not or . == "")) | .transport] | unique | .[]')
for tr in $rungs; do
  if [ "$(active_transport "$T")" != "$tr" ]; then
    dey tunnel switch "$T" --transport "$tr" --json >/dev/null 2>&1 || { log "cannot switch to $tr, skipping it"; continue; }
    is_on() { tunnel_up "$T" && [ "$(active_transport "$T")" = "$tr" ]; }
    wait_for 90 "switch to $tr" is_on
  fi
  log "probing listeners with $tr active"
  probe_all
done
leaked=$(on node1 nft -j list counter inet it_watch leak | jq '.nftables[] | select(.counter) | .counter.packets')
[ "$leaked" = 0 ] || fail "the node sent $leaked packet(s) to 8.8.8.8:53"
pass "rungs: ${rungs//$'\n'/ }"
