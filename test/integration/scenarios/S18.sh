#!/usr/bin/env bash
# S18: every rung of the ladder on its own (test-ladder): all pass, RTT reported
# The Reality rungs use the hub's real decoy sites, not the lab decoy
# (lib.sh lab_decoy): the node side of waterwall/reverse-reality verifies
# the decoy's certificate against the CA list built into Waterwall only, so
# only a real public site can pass it (and only without TLS interception).
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
if [ -n "$bad" ]; then
  # The runner prints only the tail of this log: the decoy chosen by the
  # hub and the last lines of the failing rungs' backend logs go last.
  {
    sh_on hub "grep -rho '\"serverName\": *\"[^\"]*\"' /etc/deyroute/backends/xray 2>/dev/null | sort -u | head -n 1"
    sh_on node1 "timeout 8 openssl s_client -connect www.microsoft.com:443 -servername www.microsoft.com -tls1_3 -alpn h2 </dev/null 2>&1 |
      grep -a -E 'Negotiated TLS1.3 group|Server Temp Key|ALPN|Cipher is' | head -n 3" | sed 's/^/decoy from node1: /'
    # Which decoys can carry xray/reality here: the rung again with each one.
    if [[ " $bad " == *" xray/reality "* ]]; then
      for d in dl.google.com www.speedtest.net; do
        sh_on hub "python3 - <<'PY'
import re
p = '/etc/deyroute/config.yaml'
s = re.sub(r'(?m)^  decoy_snis:.*\n(?:    - .*\n)*', '', open(p).read())
open(p, 'w').write(re.sub(r'(?m)^hub:\n', 'hub:\n  decoy_snis: [$d]\n', s, count=1))
PY"
        dey config apply --json >/dev/null 2>&1
        r=FAILED
        if dey tunnel switch "$T" --transport xray/reality --json >/dev/null 2>&1; then
          for _ in $(seq 45); do
            if tunnel_up "$T" && [ "$(active_transport "$T")" = xray/reality ]; then r=UP; break; fi
            sleep 1
          done
        fi
        echo "xray/reality with decoy $d: $r"
      done
    fi
  } >&2 || true
  fail "rungs failed: $bad"$'\n'"$(jq -r '.results[] | select(.ok | not) | .error.detail // empty' <<<"$res")"
fi
jq -e 'all(.results[] | select(.ok); .rtt_ms > 0)' <<<"$res" >/dev/null || fail "a passing rung has no RTT"
tunnel_up "$T" || wait_tunnel_up "$T" 60
pass
