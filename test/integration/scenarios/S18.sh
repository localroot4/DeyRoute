#!/usr/bin/env bash
# S18: every rung of the ladder on its own (test-ladder): all pass, RTT reported;
# the Reality rungs also come up with each of the other built-in decoys
# The Reality rungs use the hub's real decoy sites, not the lab decoy
# (lib.sh lab_decoy): the node side of waterwall/reverse-reality verifies
# the decoy's certificate against the CA list built into Waterwall only, so
# only a real public site can pass it (and only without TLS interception).
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

# set_decoys D: hub.decoy_snis = [D], applied; true when the hub's decoy
# check takes D and the xray rung is rendered with it within 60s.
set_decoys() {
  sh_on hub "python3 - <<'PY'
import re
p = '/etc/deyroute/config.yaml'
s = re.sub(r'(?m)^  decoy_snis:.*\n(?:    - .*\n)*', '', open(p).read())
open(p, 'w').write(re.sub(r'(?m)^hub:\n', 'hub:\n  decoy_snis: [$1]\n', s, count=1))
PY"
  dey config apply --json >/dev/null || fail "config apply with decoy $1 failed"
  for _ in $(seq 60); do
    sh_on hub "grep -rqs '\"serverName\": *\"$1\"' /etc/deyroute/backends/xray" && return 0
    sleep 1
  done
  return 1
}

# switch_up T R: switch tunnel T to rung R; true when it is UP on R within 45s.
switch_up() {
  dey tunnel switch "$1" --transport "$2" --json >/dev/null 2>&1 || return 1
  for _ in $(seq 45); do
    if tunnel_up "$1" && [ "$(active_transport "$1")" = "$2" ]; then return 0; fi
    sleep 1
  done
  return 1
}

setup_pair client
serve_http node1 443 1
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T" 180
res=$(dey tunnel test-ladder "$T" --yes --json) || { echo "$res" >&2; fail "test-ladder failed"; }
jq -r '.results[] | "\(.transport) ok=\(.ok) rtt=\(.rtt_ms)ms skipped=\(.skipped) \(.error.code // "")"' <<<"$res" >&2
jq -e '[.results[] | select(.skipped | not or . == "")] | length > 0' <<<"$res" >/dev/null || fail "no rung was tested"
bad=$(jq -r '[.results[] | select((.skipped | not or . == "") and (.ok | not)) | .transport] | join(" ")' <<<"$res")
if [ -n "$bad" ]; then
  sh_on hub "grep -rho '\"serverName\": *\"[^\"]*\"' /etc/deyroute/backends/xray 2>/dev/null | sort -u | head -n 1" >&2 || true
  fail "rungs failed: $bad"$'\n'"$(jq -r '.results[] | select(.ok | not) | .error.detail // empty' <<<"$res")"
fi
jq -e 'all(.results[] | select(.ok); .rtt_ms > 0)' <<<"$res" >/dev/null || fail "a passing rung has no RTT"

# The hub takes the first built-in decoy that answers (the ladder above ran
# with it). A hub that cannot reach it falls back to the next ones, so they
# must carry the Reality rungs too.
mapfile -t decoys < <(sed -n '/^var DefaultDecoySNIs/,/^}/s/^[[:space:]]*"\([^"]*\)".*/\1/p' \
  "$IT_DIR/../../internal/backend/decoy.go")
[ "${#decoys[@]}" -ge 2 ] || fail "cannot read the built-in decoys"
failed=()
for d in "${decoys[@]:1}"; do
  if ! set_decoys "$d"; then
    echo "decoy $d: not taken by the hub's decoy check" >&2; failed+=("check@$d"); continue
  fi
  for r in xray/reality waterwall/reverse-reality; do
    if switch_up "$T" "$r"; then echo "$r with decoy $d: UP" >&2; else
      echo "$r with decoy $d: FAILED" >&2; failed+=("$r@$d"); fi
  done
done
[ "${#failed[@]}" -eq 0 ] || fail "Reality rungs failed with built-in decoys: ${failed[*]}"
tunnel_up "$T" || wait_tunnel_up "$T" 60
pass
