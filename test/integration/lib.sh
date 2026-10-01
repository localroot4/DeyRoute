#!/usr/bin/env bash
# Helpers for the integration scenarios (spec section 17). A scenario is a bash
# script in scenarios/ that sources this file, drives the containers of
# compose.yml through docker and exits 0 (pass), 77 (skipped: a requirement of
# this environment is missing) or anything else (fail). run.sh sets DEY_DIST,
# DEY_VERSION, DEY_IMAGE and DEY_PROJECT.
set -euo pipefail

IT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
: "${DEY_DIST:?run scenarios through run.sh}" "${DEY_VERSION:?}" "${DEY_PROJECT:=deyit}"
export DEY_DIST DEY_VERSION DEY_PROJECT
case $(uname -m) in x86_64) ARCH=amd64 ;; aarch64 | arm64) ARCH=arm64 ;; *) ARCH=$(uname -m) ;; esac
ARCHIVE=/dist/deyroute_${DEY_VERSION}_linux_${ARCH}.tar.gz
# DEY_OFFLINE=1: the servers cannot download backends (no internet); only the
# builtin direct/native transport can carry tunnels.
DEY_OFFLINE=${DEY_OFFLINE:-0}

HUB_IP=10.77.0.10 NODE1_IP=10.77.0.11 NODE2_IP=10.77.0.12 HUB2_IP=10.77.0.13 CLIENT_IP=10.77.0.30
CONTROL_PORT=44433
export HUB_IP NODE1_IP NODE2_IP HUB2_IP CLIENT_IP CONTROL_PORT ARCH ARCHIVE

log() { printf '[%s %s] %s\n' "$(date -u +%H:%M:%S)" "${SCENARIO:-it}" "$*" >&2; }
fail() { log "FAIL: $*"; exit 1; }
skip() { log "SKIP: $*"; exit 77; }
pass() { log "PASS${1:+: $*}"; exit 0; }

dc() { docker compose -f "$IT_DIR/compose.yml" --profile hub2 "$@"; }
cid() { dc ps -a -q "$1"; }
# on SERVICE CMD...: run a command in a server (stdin is passed through).
on() { local s=$1; shift; docker exec -i "$(cid "$s")" "$@"; }
# sh_on SERVICE 'shell code': run shell code in a server.
sh_on() { local s=$1; shift; docker exec -i "$(cid "$s")" bash -c "$*"; }
# dey [--on SERVICE] ARGS...: run deyroute (default on the hub).
dey() {
  local s=hub
  if [ "${1:-}" = --on ]; then s=$2; shift 2; fi
  on "$s" deyroute "$@"
}
# expect_code CODE CMD...: the command must fail and print DEY-CODE.
expect_code() {
  local code=$1 out rc=0; shift
  out=$("$@" 2>&1) || rc=$?
  [ "$rc" -ne 0 ] || fail "expected $code, but the command succeeded: $*"$'\n'"$out"
  grep -q -- "$code" <<<"$out" || fail "expected $code from: $*"$'\n'"$out"
  printf '%s\n' "$out"
}

# wait_for SECONDS DESCRIPTION CMD...: retry CMD every second until it succeeds.
wait_for() {
  local t=$1 what=$2 end; shift 2
  end=$((SECONDS + t))
  until "$@" >/dev/null 2>&1; do
    [ "$SECONDS" -lt "$end" ] || fail "timeout after ${t}s: $what"
    sleep 1
  done
}

# up SERVICE...: start servers and wait for systemd in each.
up() {
  dc up -d --quiet-pull "$@" >&2
  local s
  for s in "$@"; do
    wait_for 90 "systemd running on $s" systemd_ready "$s"
    use_proxy "$s"
  done
}
# use_proxy SERVICE: when the lab reaches the internet only through an HTTPS
# proxy (DEY_PROXY=http://host:port, optional DEY_PROXY_CA=file of its CA),
# every service started afterwards (deyroute-hub, deyroute-node) inherits it.
use_proxy() {
  [ -n "${DEY_PROXY:-}" ] || return 0
  if [ -n "${DEY_PROXY_CA:-}" ]; then
    on "$1" sh -c 'cat > /usr/local/share/ca-certificates/it-proxy.crt && update-ca-certificates >/dev/null 2>&1' <"$DEY_PROXY_CA"
  fi
  on "$1" systemctl set-environment "HTTPS_PROXY=$DEY_PROXY" "NO_PROXY=10.77.0.0/24,127.0.0.1,localhost"
}
systemd_ready() {
  local st
  st=$(on "$1" systemctl is-system-running 2>/dev/null || true)
  [ "$st" = running ] || [ "$st" = degraded ]
}

# install_hub SERVICE NAME [installer flags...]: one-line install + setup (spec 5).
install_hub() {
  local s=$1 name=$2; shift 2
  on "$s" bash /dist/install.sh --skip-signature --local "$ARCHIVE" --role hub --name "$name" --yes "$@"
}
# join_link: a fresh join link from the hub.
join_link() { dey node join-command --json | jq -r .link; }
# install_node SERVICE [LINK] [flags...]: one-line install + join.
install_node() {
  local s=$1 link=${2:-}
  shift $(($# > 1 ? 2 : 1))
  [ -n "$link" ] || link=$(join_link)
  on "$s" bash /dist/install.sh --skip-signature --local "$ARCHIVE" join "$link" "$@"
}
node_id_of() { dey --on "$1" status --json | jq -r .node.id; }
node_online() { dey node list --json | jq -e --arg id "$1" '.nodes[] | select(.id == $id) | .online == true' >/dev/null; }
wait_node_online() { wait_for "${2:-60}" "node $1 online" node_online "$1"; }

# setup_pair: hub + node1 installed and joined (the common starting point).
setup_pair() {
  up hub node1 "$@"
  install_hub hub ir-1 >&2
  install_node node1 >&2
  NODE1=$(node_id_of node1)
  wait_node_online "$NODE1"
  export NODE1
}
# add_node2: node2 installed and joined (backup node scenarios).
add_node2() {
  up node2
  install_node node2 >&2
  NODE2=$(node_id_of node2)
  wait_node_online "$NODE2"
  export NODE2
}

# it_ladder: the ladder name tunnels use in this environment. Offline, only the
# builtin transport works, so an explicit one-rung ladder is created.
it_ladder() {
  if [ "$DEY_OFFLINE" = 1 ]; then
    dey ladder show it-direct >/dev/null 2>&1 || dey ladder create it-direct --rungs direct/native >&2
    echo it-direct
  else
    echo default
  fi
}
needs_online() { [ "$DEY_OFFLINE" != 1 ] || skip "needs internet access for backend downloads ($*)"; }

# add_tunnel NODE PORTS [flags...]: tunnel add on the hub; prints the tunnel id.
add_tunnel() {
  local n=$1 p=$2 out ladder
  shift 2
  ladder=$(it_ladder)
  out=$(dey tunnel add --node "$n" --ports "$p" --ladder "$ladder" --yes --json "$@") ||
    { printf '%s\n' "$out" >&2; fail "tunnel add --node $n --ports $p failed"; }
  jq -r .tunnel.id <<<"$out"
}
# set_failover KEY VALUE: change a failover setting of every tunnel in the hub
# config.yaml and apply it (config apply = the same path as config edit).
set_failover() {
  sh_on hub "sed -i -E 's/^( *)$1: .*/\\1$1: $2/' /etc/deyroute/config.yaml"
  dey config apply --json >/dev/null
}
# unit_of TUNNEL: the systemd unit of the active candidate.
unit_of() { tunnel_field "$1" '.rungs[] | select(.active) | .unit'; }
main_pid() { on "$1" systemctl show -p MainPID --value "$2"; }

tunnel_json() { dey tunnel show "$1" --json; }
tunnel_field() { tunnel_json "$1" | jq -r "$2"; }
tunnel_up() { [ "$(tunnel_field "$1" .state)" = UP ]; }
wait_tunnel_up() { wait_for "${2:-90}" "tunnel $1 UP" tunnel_up "$1"; }
active_transport() { tunnel_field "$1" .active_transport; }
active_node() { tunnel_field "$1" .active_node; }
has_event() { # has_event TYPE [TUNNEL]
  dey events --since 2h --json ${2:+--tunnel "$2"} | jq -e --arg t "$1" '.events[] | select(.type == $t)' >/dev/null
}
wait_event() { wait_for "${3:-60}" "event $1" has_event "$1" "${2:-}"; }

# serve_http SERVICE PORT SIZE_MB: an HTTP "VPN service" on 127.0.0.1:PORT with
# a random file /blob of SIZE_MB megabytes (the tunnel target on a node).
# The service is an enabled unit, so it survives a reboot (S15).
serve_http() {
  local s=$1 port=$2 mb=${3:-1}
  sh_on "$s" "mkdir -p /srv/it && { [ -s /srv/it/blob ] || { head -c ${mb}M /dev/urandom > /srv/it/blob && sha256sum /srv/it/blob | cut -d' ' -f1 > /srv/it/blob.sha256; }; }
    cat > /etc/systemd/system/it-http-$port.service <<EOF
[Unit]
Description=integration test service on 127.0.0.1:$port
[Service]
WorkingDirectory=/srv/it
ExecStart=/usr/bin/python3 -m http.server $port --bind 127.0.0.1
[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload && systemctl enable --now --quiet it-http-$port"
  wait_for 20 "http service on $s:$port" sh_on "$s" "ss -Hltn 'sport = :$port' | grep -q ."
}
stop_http() { sh_on "$1" "systemctl stop it-http-$2"; }
start_http() { sh_on "$1" "systemctl start it-http-$2"; }
# fetch_via_hub PORT [HOST]: download /blob through the hub from the client;
# prints the sha256 of what arrived.
fetch_via_hub() {
  sh_on client "curl -fsS --max-time 120 http://${2:-$HUB_IP}:$1/blob | sha256sum | cut -d' ' -f1"
}
blob_sha() { on "$1" cat /srv/it/blob.sha256; }

# xray_install SERVICE: the Xray release pinned in backends.yaml, downloaded
# into a server (through DEY_PROXY when set), checked against the manifest's
# sha256 and installed as /usr/local/bin/it-xray. Needs internet access.
xray_install() {
  on "$1" bash -s "$ARCH" "${DEY_PROXY:-}" <<'SH'
set -euo pipefail
[ -x /usr/local/bin/it-xray ] && exit 0
read -r url sha < <(python3 - "$1" <<'PY'
import re, sys
b = re.search(r'(?ms)^  xray:\n.*?(?=^  \S|\Z)', open('/dist/backends.yaml').read()).group(0)
print(re.search(r'%s: (https://\S+)' % sys.argv[1], b).group(1),
      re.search(r'sha256: \{[^}]*%s: "([0-9a-f]{64})"' % sys.argv[1], b).group(1))
PY
)
curl -fsSL --retry 3 ${2:+--proxy "$2"} -o /tmp/it-xray.zip "$url"
echo "$sha  /tmp/it-xray.zip" | sha256sum -c --quiet
python3 -c 'import zipfile; zipfile.ZipFile("/tmp/it-xray.zip").extract("xray", "/tmp/it-xray.d")'
install -m 0755 /tmp/it-xray.d/xray /usr/local/bin/it-xray
SH
}
# it_unit SERVICE NAME COMMAND: an enabled unit it-NAME running COMMAND (it
# survives a reboot, like the services of serve_http).
it_unit() {
  sh_on "$1" "cat > /etc/systemd/system/it-$2.service <<EOF
[Service]
ExecStart=$3
Restart=always
[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload && systemctl enable --now --quiet it-$2"
}

# vpn_up NODE PORT [SIZE_MB]: the user's VPN behind the tunnel port PORT, as
# in spec section 17: a real Xray VLESS+ws+tls server on NODE 127.0.0.1:PORT
# (the tunnel target; certificate for vpn.it.lab, which the client trusts)
# whose "internet" is the serve_http web server on NODE 127.0.0.1:8080, and a
# real Xray client on the client that dials hub:PORT and offers an HTTP proxy
# on 127.0.0.1:10809 for curl --proxy. Offline (Xray cannot be downloaded)
# the web server listens on NODE 127.0.0.1:PORT itself and the client requests
# hub:PORT directly. Sets VPN_URL and VPN_CURL (curl's proxy options, ""
# offline) for fetch_via_vpn and start_prober. curl tunnels through the proxy
# (CONNECT): Xray's plain-HTTP proxying fails short responses that the server
# closes right away (503, "read/write on closed pipe").
VPN_UUID=6f1c4a52-3b9e-4d1a-9c55-2e8a6b0d4f13 VPN_WEB_PORT=8080 VPN_PROXY_PORT=10809
vpn_up() {
  local n=$1 port=$2 mb=${3:-1} crt
  if [ "$DEY_OFFLINE" = 1 ]; then
    log "offline: no Xray download, the client requests hub:$port without the VPN client"
    serve_http "$n" "$port" "$mb"
    VPN_URL=http://$HUB_IP:$port VPN_CURL=""
    return
  fi
  serve_http "$n" "$VPN_WEB_PORT" "$mb"
  xray_install "$n" >&2
  xray_install client >&2
  sh_on "$n" "mkdir -p /etc/it-xray && { [ -s /etc/it-xray/vpn.crt ] || openssl req -x509 -newkey ec \
    -pkeyopt ec_paramgen_curve:P-256 -nodes -days 2 -subj /CN=vpn.it.lab -addext subjectAltName=DNS:vpn.it.lab \
    -keyout /etc/it-xray/vpn.key -out /etc/it-xray/vpn.crt 2>/dev/null; }
    cat > /etc/it-xray/server.json <<EOF
{\"log\": {\"loglevel\": \"warning\", \"access\": \"/var/log/it-xray-access.log\"},
 \"inbounds\": [{\"tag\": \"vless-in\", \"listen\": \"127.0.0.1\", \"port\": $port, \"protocol\": \"vless\",
   \"settings\": {\"clients\": [{\"id\": \"$VPN_UUID\"}], \"decryption\": \"none\"},
   \"streamSettings\": {\"network\": \"ws\", \"security\": \"tls\", \"wsSettings\": {\"path\": \"/it-vless\"},
     \"tlsSettings\": {\"certificates\": [{\"certificateFile\": \"/etc/it-xray/vpn.crt\", \"keyFile\": \"/etc/it-xray/vpn.key\"}]}}}],
 \"outbounds\": [{\"protocol\": \"freedom\"}]}
EOF"
  it_unit "$n" xray-server "/usr/local/bin/it-xray run -c /etc/it-xray/server.json"
  crt=$(on "$n" cat /etc/it-xray/vpn.crt)
  on client sh -c 'cat > /usr/local/share/ca-certificates/it-vpn.crt && update-ca-certificates >/dev/null 2>&1' <<<"$crt"
  sh_on client "mkdir -p /etc/it-xray && cat > /etc/it-xray/client.json <<EOF
{\"log\": {\"loglevel\": \"warning\"},
 \"inbounds\": [{\"listen\": \"127.0.0.1\", \"port\": $VPN_PROXY_PORT, \"protocol\": \"http\"}],
 \"outbounds\": [{\"protocol\": \"vless\",
   \"settings\": {\"vnext\": [{\"address\": \"$HUB_IP\", \"port\": $port, \"users\": [{\"id\": \"$VPN_UUID\", \"encryption\": \"none\"}]}]},
   \"streamSettings\": {\"network\": \"ws\", \"security\": \"tls\", \"wsSettings\": {\"path\": \"/it-vless\"},
     \"tlsSettings\": {\"serverName\": \"vpn.it.lab\"}}}]}
EOF"
  it_unit client xray-client "/usr/local/bin/it-xray run -c /etc/it-xray/client.json"
  wait_for 20 "xray server on $n:$port" sh_on "$n" "ss -Hltn 'sport = :$port' | grep -q ."
  wait_for 20 "xray client on the client" sh_on client "ss -Hltn 'sport = :$VPN_PROXY_PORT' | grep -q ."
  VPN_URL=http://127.0.0.1:$VPN_WEB_PORT VPN_CURL="--proxy http://127.0.0.1:$VPN_PROXY_PORT --proxytunnel"
}
# fetch_via_vpn [PATH]: download PATH (default /blob) through the user's VPN
# (vpn_up) from the client; prints the sha256 of what arrived.
fetch_via_vpn() {
  sh_on client "curl -fsS --max-time 120 ${VPN_CURL:-} $VPN_URL${1:-/blob} | sha256sum | cut -d' ' -f1"
}
# vpn_tls_errors: TLS/certificate errors logged by the Xray server and client.
vpn_tls_errors() {
  local s n=${1:-node1}
  for s in client "$n"; do
    sh_on "$s" "journalctl --no-pager -q -u 'it-xray-*' | grep -i -E 'tls|x509|certificate|handshake' || true"
  done
}

# udp_echo SERVICE PORT: a UDP echo service on 127.0.0.1:PORT ("echo:" + the
# datagram); udp_ask HOST PORT prints the reply to one datagram from the client.
udp_echo() {
  it_unit "$1" "udp-echo-$2" "/usr/bin/python3 -c 'import socket; s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); \
s.bind((\"127.0.0.1\", $2)); [s.sendto(b\"echo:\" + d, a) for d, a in iter(lambda: s.recvfrom(65535), None)]'"
}
udp_ask() {
  on client python3 - "$1" "$2" <<'PY'
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(2)
for _ in range(5):
    s.sendto(b"it-ping", (sys.argv[1], int(sys.argv[2])))
    try:
        print(s.recvfrom(65535)[0].decode()); break
    except OSError:
        pass
PY
}

# start_prober [PORT]: the client requests the VPN's web server through the
# Xray client (vpn_up) — or http://hub:PORT/ when there is no VPN — every
# 200ms and logs one line per request (epoch-ms and ok|fail); stop_prober
# prints the longest outage in milliseconds. The unit clears the NO_PROXY of
# use_proxy, which would send 127.0.0.1 around the VPN's proxy.
start_prober() {
  local url=${VPN_URL:-http://$HUB_IP:$1} px=${VPN_CURL:-}
  sh_on client "systemctl stop it-prober 2>/dev/null; rm -f /tmp/prober.log
    systemd-run --quiet --unit it-prober -E NO_PROXY= -E no_proxy= bash -c 'while :; do
      if curl -fsS -o /dev/null --max-time 1 $px $url/; then r=ok; else r=fail; fi
      echo \"\$(date +%s%3N) \$r\" >> /tmp/prober.log; sleep 0.2; done'"
}
stop_prober() {
  sh_on client "systemctl stop it-prober 2>/dev/null || true"
  on client cat /tmp/prober.log | awk '
    $2 == "fail" { if (!start) start = $1; last = $1; next }
    { if (start) { d = $1 - start; if (d > max) max = d; start = 0 } }
    END { if (start) { d = last - start + 200; if (d > max) max = d }; print max + 0 }'
}

# block [--node IP] PROTO PORT / block --host IP / block --udp IP / unblock: emulate a filter
# between the hub and the nodes with an nftables table in the hub's network
# namespace (the "blocker" of spec section 17). Established flows are cut too.
block() {
  local sel="" rules=""
  sh_on hub "nft list table inet it_blocker >/dev/null 2>&1 || nft -f - <<'EOF'
table inet it_blocker {
  chain in { type filter hook input priority -50; }
  chain out { type filter hook output priority -50; }
}
EOF"
  if [ "$1" = --host ]; then
    rules="add rule inet it_blocker in ip saddr $2 drop
add rule inet it_blocker out ip daddr $2 drop"
  elif [ "$1" = --udp ]; then
    rules="add rule inet it_blocker in ip saddr $2 meta l4proto udp drop
add rule inet it_blocker out ip daddr $2 meta l4proto udp drop"
  else
    if [ "$1" = --node ]; then sel="ip saddr $2 "; shift 2; fi
    local p=$1 port=$2 osel=${sel/saddr/daddr}
    rules="add rule inet it_blocker in ${sel}meta l4proto $p th dport $port drop
add rule inet it_blocker in ${sel}meta l4proto $p th sport $port drop
add rule inet it_blocker out ${osel}meta l4proto $p th dport $port drop
add rule inet it_blocker out ${osel}meta l4proto $p th sport $port drop"
  fi
  on hub nft -f - <<<"$rules"
}
unblock() { sh_on hub "nft delete table inet it_blocker 2>/dev/null || true"; }

# https_mirror: an HTTPS file server on https://$CLIENT_IP:8443/ serving
# /srv/mirror of the client, with a lab certificate the hub and the nodes trust.
https_mirror() {
  sh_on client "mkdir -p /srv/mirror && cd /srv/mirror &&
    [ -s /etc/it-mirror.crt ] || openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 2 \
      -subj /CN=it-mirror -addext 'subjectAltName=IP:$CLIENT_IP' -keyout /etc/it-mirror.key -out /etc/it-mirror.crt 2>/dev/null
    systemctl stop it-mirror 2>/dev/null || true
    systemd-run --quiet --unit it-mirror -p WorkingDirectory=/srv/mirror python3 -c '
import http.server, ssl
s = http.server.ThreadingHTTPServer((\"0.0.0.0\", 8443), http.server.SimpleHTTPRequestHandler)
c = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER); c.load_cert_chain(\"/etc/it-mirror.crt\", \"/etc/it-mirror.key\")
s.socket = c.wrap_socket(s.socket, server_side=True); s.serve_forever()'"
  wait_for 20 "https mirror up" sh_on client "ss -Hltn 'sport = :8443' | grep -q ."
  local crt s
  crt=$(on client cat /etc/it-mirror.crt)
  for s in hub node1 node2; do
    [ -n "$(cid "$s")" ] || continue
    on "$s" sh -c 'cat > /usr/local/share/ca-certificates/it-mirror.crt && update-ca-certificates >/dev/null 2>&1' <<<"$crt"
  done
}
# fake_backend_archive NAME SCRIPT: /srv/mirror/<NAME>.tar.gz on the client with
# one executable NAME whose body is SCRIPT; prints its sha256.
fake_backend_archive() {
  on client sh -c "mkdir -p /tmp/fb && cat > /tmp/fb/$1 && chmod 0755 /tmp/fb/$1 &&
    tar -C /tmp/fb -czf /srv/mirror/$1.tar.gz $1 && sha256sum /srv/mirror/$1.tar.gz | cut -d' ' -f1" <<<"$2"
}
# override_backend NAME VERSION URL SHA256: install a manifest override on the
# hub announcing NAME at VERSION from URL, and restart the hub to load it.
override_backend() {
  on hub python3 - "$1" "$2" "$3" "$4" <<'PY'
import re, sys
name, ver, url, sha = sys.argv[1:]
src = open('/dist/backends.yaml').read()
m = re.search(r'(?ms)^  %s:\n.*?(?=^  \S|\Z)' % re.escape(name), src)
b = m.group(0)
b2 = re.sub(r'version: \S+', 'version: ' + ver, b, count=1)
b2 = re.sub(r'(amd64|arm64): https?://\S+', lambda x: x.group(1) + ': ' + url, b2)
b2 = re.sub(r'sha256: \{[^}]*\}', 'sha256: { amd64: "%s", arm64: "%s" }' % (sha, sha), b2)
open('/etc/deyroute/backends.yaml', 'w').write(src.replace(b, b2))
PY
  on hub systemctl restart deyroute-hub
  wait_for 60 "hub back after the manifest override" sh_on hub "deyroute status --json >/dev/null"
}

# secrets_of SERVICE: every secret value on a server (tokens, keys, passwords),
# one per line, for the "no secret in any log" checks (S26).
secrets_of() {
  sh_on "$1" "find /etc/deyroute/secrets -type f ! -name '*.crt' -print0 2>/dev/null | xargs -0 -r cat" |
    grep -v -e '^-----' | tr -s ' \t:="{},' '\n' | awk 'length($0) >= 16' | sort -u
}
# join_token LINK: the token part of dey://TOKEN@HOST:PORT#fp.
join_token() { local t=${1#dey://}; echo "${t%%@*}"; }

# diagnostics DIR: everything useful after a failure.
diagnostics() {
  local dir=$1 s
  mkdir -p "$dir"
  for s in hub node1 node2 hub2 client; do
    [ -n "$(cid "$s" 2>/dev/null)" ] || continue
    sh_on "$s" "deyroute status 2>&1; echo; nft list ruleset 2>&1; echo; systemctl list-units --all 'deyroute*' --no-pager 2>&1;
      echo; journalctl --no-pager -n 400 -u 'deyroute*' 2>&1; echo; tail -n 300 /var/log/deyroute/*.log /var/log/deyroute/tunnels/*.log 2>/dev/null" \
      >"$dir/$s.txt" 2>&1 || true
  done
  dc logs --no-color >"$dir/compose.txt" 2>&1 || true
}
