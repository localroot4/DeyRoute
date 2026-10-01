#!/usr/bin/env bash
# S18: every rung of the ladder on its own (test-ladder): all pass, RTT reported
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair client
serve_http node1 443 1
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T" 180
res=$(dey tunnel test-ladder "$T" --yes --json) || { echo "$res" >&2; fail "test-ladder failed"; }
jq -r '.results[] | "\(.transport) ok=\(.ok) rtt=\(.rtt_ms)ms skipped=\(.skipped) \(.error.code // "")"' <<<"$res" >&2
jq -e '[.results[] | select(.skipped | not or . == "")] | length > 0' <<<"$res" >/dev/null || fail "no rung was tested"
bad=$(jq -r '[.results[] | select((.skipped | not or . == "") and (.ok | not)) | .transport] | join(" ")' <<<"$res")
[ -z "$bad" ] || fail "rungs failed: $bad"
jq -e 'all(.results[] | select(.ok); .rtt_ms > 0)' <<<"$res" >/dev/null || fail "a passing rung has no RTT"
tunnel_up "$T" || wait_tunnel_up "$T" 60
pass
