#!/usr/bin/env bash
# S40: advanced.proxy_protocol on an frp rung: the service on the node sees the client's IP (PROXY protocol v1), the tunnel stays UP
# Not one of the spec's S01-S30 (DR-21). The node's service on 127.0.0.1:8443
# answers every HTTP request with the address it sees: the source of a
# leading PROXY v1 line when there is one, else the peer address. On
# frp/tcp without the setting it sees a local address (masked); with
# advanced.proxy_protocol frpc sends the PROXY line, the service sees the
# client container's address, tunnel show says client_ip preserved, and the
# tunnel's probes (whose helpers on the node skip the line) keep it UP.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "FRP"

setup_pair client
sh_on node1 "cat > /usr/local/bin/it-seen-ip.py <<'PY'
import asyncio
async def handle(r, w):
    try:
        line = await asyncio.wait_for(r.readline(), 5)
        ip = w.get_extra_info('peername')[0]
        if line.startswith(b'PROXY '):
            ip = line.split()[2].decode()
            line = await asyncio.wait_for(r.readline(), 5)
        while line not in (b'\r\n', b'\n', b''):
            line = await asyncio.wait_for(r.readline(), 5)
        w.write(b'HTTP/1.0 200 OK\r\nContent-Type: text/plain\r\n\r\n' + ip.encode() + b'\n')
        await w.drain()
    except Exception:
        pass
    w.close()
async def main():
    s = await asyncio.start_server(handle, '127.0.0.1', 8443)
    async with s:
        await s.serve_forever()
asyncio.run(main())
PY"
it_unit node1 seen-ip "/usr/bin/python3 /usr/local/bin/it-seen-ip.py"
wait_for 20 "seen-ip service on node1" sh_on node1 "ss -Hltn 'sport = :8443' | grep -q ."
seen() { sh_on client "curl -fsS --max-time 20 http://$HUB_IP:8443/"; }

T=$(add_tunnel "$NODE1" 8443 --ladder frp/tcp)
wait_tunnel_up "$T" 180
[ "$(active_transport "$T")" = frp/tcp ] || fail "the tunnel is on $(active_transport "$T"), want frp/tcp"
ip=$(seen) || fail "no answer through the tunnel"
[ "$ip" != "$CLIENT_IP" ] || fail "the service saw the client's IP without proxy_protocol"
[ "$(tunnel_field "$T" .client_ip)" = masked ] || fail "client_ip without proxy_protocol: $(tunnel_field "$T" .client_ip)"
log "without proxy_protocol the service sees $ip"

sh_on hub "python3 - <<'PY'
p = '/etc/deyroute/config.yaml'
s = open(p).read()
k = '  - id: $T\n'
assert k in s and 'proxy_protocol' not in s
open(p, 'w').write(s.replace(k, k + '    advanced:\n      proxy_protocol: true\n', 1))
PY"
dey config apply --json >/dev/null || fail "config apply with proxy_protocol failed"
sees_client() { [ "$(seen 2>/dev/null)" = "$CLIENT_IP" ]; }
wait_for 90 "the service sees the client's IP" sees_client
[ "$(tunnel_field "$T" .client_ip)" = preserved ] || fail "client_ip with proxy_protocol: $(tunnel_field "$T" .client_ip)"
sh_on node1 "grep -rqs --include=frpc.toml 'proxyProtocolVersion = \"v1\"' /etc/deyroute /var/lib/deyroute" ||
  fail "no frpc.toml on node1 has proxyProtocolVersion"

# The probes keep the tunnel UP on the same rung for 30 s.
end=$((SECONDS + 30))
while [ "$SECONDS" -lt "$end" ]; do
  tunnel_up "$T" || fail "tunnel $T left UP with proxy_protocol ($(tunnel_field "$T" .state))"
  [ "$(active_transport "$T")" = frp/tcp ] || fail "tunnel $T left frp/tcp with proxy_protocol"
  sleep 3
done
pass "frp/tcp: masked $ip, then the client's $CLIENT_IP with proxy_protocol; UP throughout"
