#!/usr/bin/env bash
# S29: a tunnel with 64 port maps given as a range: rendered correctly, every port probed
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
# One service answering on 127.0.0.1:20000-20063 with its own port number.
sh_on node1 "cat > /usr/local/bin/it-multi <<'PY'
import http.server, threading
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        b = str(self.server.server_address[1]).encode()
        self.send_response(200); self.send_header('Content-Length', str(len(b))); self.end_headers(); self.wfile.write(b)
    def log_message(self, *a): pass
for p in range(20000, 20064):
    s = http.server.ThreadingHTTPServer(('127.0.0.1', p), H)
    threading.Thread(target=s.serve_forever, daemon=True).start()
threading.Event().wait()
PY
systemd-run --quiet --unit it-multi python3 /usr/local/bin/it-multi"
wait_for 20 "64 services up" sh_on node1 "[ \$(ss -Hltn 'sport >= :20000 and sport <= :20063' | wc -l) -eq 64 ]"

T=$(add_tunnel "$NODE1" 20000-20063)
wait_tunnel_up "$T" 120
n=$(tunnel_field "$T" '.ports | length')
[ "$n" = 64 ] || fail "the tunnel has $n port maps, want 64"
bad=$(sh_on client "for p in \$(seq 20000 20063); do [ \"\$(curl -fsS --max-time 5 http://$HUB_IP:\$p/)\" = \"\$p\" ] || echo \$p; done")
[ -z "$bad" ] || fail "ports not carried correctly: $bad"
pr=$(dey diag probe "$T" --all-ports --json)
jq -e '(.probes | length) == 64 and all(.probes[]; .ok)' <<<"$pr" >/dev/null || fail "diag probe: $(jq -c '[.probes[] | select(.ok | not)]' <<<"$pr")"
expect_code DEY- dey port add "$T" 20064 >/dev/null # a 65th port map is refused
pass
