#!/usr/bin/env bash
# S25: 500 concurrent client connections over Backhaul: backend RAM <= 150 MB, no errors
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "backhaul is downloaded"

setup_pair client
serve_http node1 443 1
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T" 180
dey tunnel switch "$T" --transport backhaul/tcpmux --json >/dev/null
on_bh() { tunnel_up "$T" && [ "$(active_transport "$T")" = backhaul/tcpmux ]; }
wait_for 90 "tunnel on backhaul/tcpmux" on_bh
unit=$(unit_of "$T")

res=$(on client python3 - "$HUB_IP" 443 500 <<'PY'
import socket, sys, threading, time
host, port, n = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
ok = err = 0
lock = threading.Lock()
barrier = threading.Barrier(n)
def worker():
    global ok, err
    try:
        s = socket.create_connection((host, port), timeout=30)
        barrier.wait(timeout=60)  # all 500 open at the same time
        s.sendall(b"GET /blob HTTP/1.0\r\nHost: x\r\n\r\n")
        got = 0
        while True:
            b = s.recv(65536)
            if not b:
                break
            got += len(b)
        s.close()
        good = got > 1024 * 1024
    except Exception:
        good = False
    with lock:
        if good: ok += 1
        else: err += 1
ts = [threading.Thread(target=worker) for _ in range(n)]
for t in ts: t.start()
for t in ts: t.join()
print(ok, err)
PY
)
read -r ok err <<<"$res"
mem() { on "$1" systemctl show -p MemoryPeak --value "$unit" 2>/dev/null | grep -E '^[0-9]+$' || on "$1" systemctl show -p MemoryCurrent --value "$unit"; }
mh=$(mem hub) mn=$(mem node1)
log "ok=$ok err=$err, backend memory: hub $((mh / 1048576)) MB, node $((mn / 1048576)) MB"
{ [ "$err" = 0 ] && [ "$ok" = 500 ]; } || fail "$err of 500 connections failed"
[ "$mh" -le $((150 * 1048576)) ] || fail "hub backend used $((mh / 1048576)) MB"
[ "$mn" -le $((150 * 1048576)) ] || fail "node backend used $((mn / 1048576)) MB"
pass
