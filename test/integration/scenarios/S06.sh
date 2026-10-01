#!/usr/bin/env bash
# S06: hub without GitHub access: backend binaries arrive through the node, tunnel UP
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "backends are downloaded by the node"

setup_pair client
# Cut the hub off the internet (only the lab network stays reachable).
on hub nft -f - <<'EOF'
table inet it_nogithub {
  chain out {
    type filter hook output priority -50;
    ip daddr != { 10.77.0.0/24, 127.0.0.0/8 } drop
  }
}
EOF
on hub timeout 5 curl -fsS -o /dev/null https://github.com 2>/dev/null && fail "the hub still reaches github.com"

serve_http node1 443 1
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T" 180
tr=$(active_transport "$T")
case $tr in direct/*) fail "only the builtin transport came up ($tr): no backend arrived via the node" ;; esac
[ "$(fetch_via_hub 443)" = "$(blob_sha node1)" ] || fail "download through the tunnel failed"
pass "via $tr"
