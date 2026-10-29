#!/usr/bin/env bash
# S38: silent raw-TCP targets (VMess tcp, SS-2022, VLESS tcp; no TLS) come UP with the default probe and still switch <= 35s when the active rung is blocked
# The pinned Xray serves three inbounds on node1 (DR-17). They never answer
# the TLS hello of the auto probe the way a TLS or HTTP service does:
# - VMess tcp security=none with a "large drain" UUID: after a failed
#   authentication Xray drains a per-server number of bytes (a seed derived
#   from its users) before it closes; for this UUID that is more than the
#   probe's ClientHello, so the connection stays silent until Xray's own
#   handshake timeout. Measured against the pinned Xray v26.3.27 on
#   127.0.0.1: health.Path(..., "auto", 3s) timed out (also with 30s, and
#   the tls kind with 6s), stable over Xray restarts; 6 of 12 random UUIDs
#   did, the others closed at once;
# - Shadowsocks-2022 (2022-blake3-aes-128-gcm): the connection is reset;
# - VLESS tcp security=none: closes at once without a byte (the hub's
#   "closes" verdict already handled it before DR-17).
# Each tunnel is added with the defaults and must reach UP: the hub learns
# the silent verdict on node1 and probes VMess and SS-2022 end to end with
# the echo probe (probe_effective "echo"), VLESS stays on auto. A real Xray
# VMess client on the client fetches through the VMess tunnel. Then, as in
# S08, the active rung of each tunnel is blocked: it must be UP on another
# rung within 35s (the echo probe fails on a black-holed rung like any
# other probe).
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "the pinned Xray and a ladder with more than one rung"

# Found with the spike of DR-17 (see above); keep it, another UUID may close.
VMESS_UUID=971f3426-8771-44eb-a2c7-04a65e9cecd5
VLESS_UUID=0b6f0c55-6a3e-4b8f-9a47-3c0d9e5f2a71
SS_KEY=MDEyMzQ1Njc4OWFiY2RlZg== # 16 bytes, base64 (2022-blake3-aes-128-gcm)
P_VMESS=10101 P_SS=10102 P_VLESS=10103 WEB=8080 PROXY=10811

setup_pair client
serve_http node1 "$WEB" 1
xray_install node1 >&2
xray_install client >&2
sh_on node1 "mkdir -p /etc/it-xray && cat > /etc/it-xray/silent.json <<EOF
{\"log\": {\"loglevel\": \"warning\"},
 \"inbounds\": [
  {\"listen\": \"127.0.0.1\", \"port\": $P_VMESS, \"protocol\": \"vmess\",
   \"settings\": {\"clients\": [{\"id\": \"$VMESS_UUID\"}]}, \"streamSettings\": {\"network\": \"tcp\", \"security\": \"none\"}},
  {\"listen\": \"127.0.0.1\", \"port\": $P_SS, \"protocol\": \"shadowsocks\",
   \"settings\": {\"method\": \"2022-blake3-aes-128-gcm\", \"password\": \"$SS_KEY\", \"network\": \"tcp\"}},
  {\"listen\": \"127.0.0.1\", \"port\": $P_VLESS, \"protocol\": \"vless\",
   \"settings\": {\"clients\": [{\"id\": \"$VLESS_UUID\"}], \"decryption\": \"none\"},
   \"streamSettings\": {\"network\": \"tcp\", \"security\": \"none\"}}],
 \"outbounds\": [{\"protocol\": \"freedom\"}]}
EOF"
it_unit node1 xray-silent "/usr/local/bin/it-xray run -c /etc/it-xray/silent.json"
for p in $P_VMESS $P_SS $P_VLESS; do
  wait_for 20 "xray inbound on node1:$p" sh_on node1 "ss -Hltn 'sport = :$p' | grep -q ."
done

declare -A T WANT=([$P_VMESS]=echo [$P_SS]=echo [$P_VLESS]=auto)
for p in $P_VMESS $P_SS $P_VLESS; do
  T[$p]=$(add_tunnel "$NODE1" "$p")
done
for p in $P_VMESS $P_SS $P_VLESS; do
  wait_tunnel_up "${T[$p]}" 180
  eff=$(tunnel_field "${T[$p]}" '.ports[0].probe_effective')
  log "port $p: UP on $(active_transport "${T[$p]}"), probe ${eff}: $(tunnel_field "${T[$p]}" '.ports[0].probe_reason')"
  [ "$eff" = "${WANT[$p]}" ] || fail "port $p: probe_effective $eff, want ${WANT[$p]}"
  [ "$(tunnel_field "${T[$p]}" '.ports | length')" = 1 ] || fail "port $p: the hidden echo port shows up as a port map"
done
set_failover failback false

# Real traffic: a VMess client on the client through hub:P_VMESS.
sh_on client "mkdir -p /etc/it-xray && cat > /etc/it-xray/vmess.json <<EOF
{\"log\": {\"loglevel\": \"warning\"},
 \"inbounds\": [{\"listen\": \"127.0.0.1\", \"port\": $PROXY, \"protocol\": \"http\"}],
 \"outbounds\": [{\"protocol\": \"vmess\",
   \"settings\": {\"vnext\": [{\"address\": \"$HUB_IP\", \"port\": $P_VMESS, \"users\": [{\"id\": \"$VMESS_UUID\", \"security\": \"aes-128-gcm\"}]}]},
   \"streamSettings\": {\"network\": \"tcp\", \"security\": \"none\"}}]}
EOF"
it_unit client xray-vmess "/usr/local/bin/it-xray run -c /etc/it-xray/vmess.json"
wait_for 20 "xray vmess client" sh_on client "ss -Hltn 'sport = :$PROXY' | grep -q ."
vmess_fetch() {
  sh_on client "NO_PROXY= no_proxy= curl -fsS --max-time 60 --proxy http://127.0.0.1:$PROXY --proxytunnel \
    http://127.0.0.1:$WEB/blob | sha256sum | cut -d' ' -f1"
}
[ "$(vmess_fetch)" = "$(blob_sha node1)" ] || fail "the VMess client got a different /blob through the tunnel"

# A block of the active rung is still found (S08 limits).
for p in $P_VMESS $P_SS $P_VLESS; do
  t=${T[$p]}
  from=$(active_transport "$t")
  port=$(tunnel_field "$t" '.rungs[] | select(.active) | .control_port')
  [ "${port:-0}" -gt 0 ] || fail "port $p: active rung $from has no control port"
  t0=$SECONDS
  block --node "$NODE1_IP" tcp "$port"
  block --node "$NODE1_IP" udp "$port"
  switched() { tunnel_up "$t" && [ "$(active_transport "$t")" != "$from" ]; }
  wait_for 90 "port $p: switch away from $from" switched
  took=$((SECONDS - t0))
  log "port $p: $from -> $(active_transport "$t") in ${took}s"
  [ "$took" -le 35 ] || fail "port $p: switch took ${took}s > 35s"
  unblock
done
[ "$(vmess_fetch)" = "$(blob_sha node1)" ] || fail "the VMess client got a different /blob after the switch"
has_event switch_transport "${T[$P_VMESS]}" || fail "no switch_transport event"
pass "VMess and SS-2022 on the echo probe, VLESS on auto; every switch <= 35s"
