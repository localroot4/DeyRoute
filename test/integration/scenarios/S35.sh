#!/usr/bin/env bash
# S35: passes, then cut: flows of a rung's control port die after 12 packets; switch time, states, switches and client outage measured
# A measurement, not an acceptance test (the filtering report from Iran of
# 2026-10-17): no rung is asserted to get through such a filter. The cut is
# lib.sh block --cut-after on the hub: a flow to or from the port is dropped
# once conntrack has counted 12 packets in its original direction, and
# every new flow passes again for 12 packets. Accounting is on before the
# tunnels start, so the flows they already have are cut as well.
#  (a) The default ladder is UP and its active rung's control port is cut;
#      for 120 s the Xray client downloads at 256 kB/s and the prober
#      requests every 200 ms. Recorded: the time to the first switch (or
#      none), the state sequence, the number of switches, the client's
#      longest outage.
#  (b) test-ladder with another rung's control port cut: that rung reports
#      DEY-B073 (passed the probe, then failed). A result without an error
#      code is logged, not failed; so is another code from a build that
#      does not know DEY-B073.
#  (c) A UDP-only tunnel with its active rung's UDP control port cut: its
#      probe checks its units only, so it is expected to stay UP while the
#      echoes through it go unanswered (the blind spot, recorded).
# Otherwise it fails only when the CLI hangs, a daemon crashes or restarts,
# or the engine stays in SWITCHING. The numbers go to $ART/S35-measure.txt,
# the state samples to $ART/S35-a-states.txt and $ART/S35-c-states.txt, the
# test-ladder result to $ART/S35-b-test-ladder.json.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "a ladder with more than one rung"

CUT=${S35_CUT_AFTER:-12} WINDOW=${S35_WINDOW_S:-120}
MEASURE=$ART/S35-measure.txt
: >"$MEASURE"
# note LINE: one line of the measurement, to the log and to $MEASURE.
note() { log "$*"; printf '%s\n' "$*" >>"$MEASURE"; }

# sample T: "STATE TRANSPORT" of tunnel T now; "HANG -" when tunnel show
# does not answer within 20 s, "ERR -" when it fails.
sample() {
  local out rc=0
  out=$(on hub timeout 20 deyroute tunnel show "$1" --json 2>/dev/null) || rc=$?
  if [ "$rc" = 124 ]; then echo "HANG -"; return; fi
  if [ "$rc" != 0 ] || [ -z "$out" ]; then echo "ERR -"; return; fi
  jq -r '"\(.state) \((.active_transport | select(. != null and . != "")) // "-")"' <<<"$out"
}
# watch_states T SECONDS FILE: sample T about once a second for SECONDS;
# one line per sample in FILE: seconds since the start, state, transport.
watch_states() {
  local t0=$SECONDS
  : >"$3"
  while [ $((SECONDS - t0)) -lt "$2" ]; do
    printf '%s %s\n' $((SECONDS - t0)) "$(sample "$1")" >>"$3"
    sleep 1
  done
}
# sequence FILE: the samples of FILE where they change ("UP x @0s, ...").
sequence() {
  awk '{ k = $2 " " $3 } k != last { printf "%s%s @%ss", sep, k, $1; sep = ", "; last = k } END { print "" }' "$1"
}
# longest STATE FILE: the longest stretch of FILE in STATE, in seconds.
longest() {
  awk -v s="$1" '$2 == s && !on { from = $1; on = 1 }
    $2 != s && on { if ($1 - from > max) max = $1 - from; on = 0 }
    { last = $1 } END { if (on && last - from > max) max = last - from; print max + 0 }' "$2"
}
# count STATE FILE: the samples of FILE in STATE.
count() { awk -v s="$1" '$2 == s { n++ } END { print n + 0 }' "$2"; }

# last_seq T: the number of tunnel T's newest event (0: none).
last_seq() { dey events --since 2h --json --tunnel "$1" | jq '[.events[].seq] | max // 0'; }
# events_after T SEQ: the types of T's events after event SEQ, oldest first.
events_after() {
  dey events --since 2h --json --tunnel "$1" |
    jq -r --argjson s "$2" '[.events | sort_by(.seq)[] | select(.seq > $s) | .type] | join(", ")'
}
# switches_after T SEQ: T's switch_transport and switch_node events after
# event SEQ, oldest first, one per line: epoch-ms, from, to.
switches_after() {
  local at from to
  dey events --since 2h --json --tunnel "$1" |
    jq -r --argjson s "$2" '.events | sort_by(.seq)[] | select(.seq > $s) |
      select(.type == "switch_transport" or .type == "switch_node") |
      "\(.at) \(.from_transport // "-") \(.to_transport // "-")"' |
    while read -r at from to; do echo "$(date -d "$at" +%s%3N) $from $to"; done
}

# dl_start SECONDS: the client downloads /blob through the VPN (vpn_up) at
# 256 kB/s, one download after another, for SECONDS; one line per download
# in /tmp/s35-dl.log: start and end (epoch-ms), curl's exit code, bytes.
dl_start() {
  on client sh -c 'cat > /usr/local/bin/it-s35-dl && chmod 0755 /usr/local/bin/it-s35-dl' <<'SH'
#!/bin/bash
end=$(($(date +%s) + $1)) url=$2
shift 2
while left=$((end - $(date +%s))) && [ "$left" -gt 0 ]; do
  s=$(date +%s%3N)
  n=$(curl -sS -o /dev/null --limit-rate 256k --max-time "$left" -w '%{size_download}' "$@" "$url" 2>/dev/null)
  rc=$?
  echo "$s $(date +%s%3N) $rc ${n:-0}" >>/tmp/s35-dl.log
  [ "$rc" = 0 ] || sleep 1
done
SH
  sh_on client "systemctl stop it-s35-dl 2>/dev/null; rm -f /tmp/s35-dl.log
    systemd-run --quiet --unit it-s35-dl -E NO_PROXY= -E no_proxy= /usr/local/bin/it-s35-dl $1 $VPN_URL/blob ${VPN_CURL:-}"
}
# dl_summary END_MS: the downloads of dl_start; one still running at END_MS
# (stopped by its --max-time, curl exit 28) was not interrupted.
dl_summary() {
  { on client cat /tmp/s35-dl.log 2>/dev/null || true; } | awk -v end="$1" '
    { b += $4; if ($3 == 0) ok++; else if (!($3 == 28 && $2 >= end - 2000)) cut++ }
    END { printf "%d downloads, %d complete, %d interrupted, %.1f MB in total\n", NR, ok, cut, b / 1048576 }'
}
# probes: how many of the prober's requests failed.
probes() {
  { on client cat /tmp/prober.log 2>/dev/null || true; } |
    awk '$2 == "fail" { f++ } END { printf "%d of %d prober requests failed\n", f, NR }'
}

# alive PART: neither daemon crashed or restarted since the start.
alive() {
  local h n
  h=$(main_pid hub deyroute-hub) n=$(main_pid node1 deyroute-node)
  [ "$h" = "$hub_pid" ] || fail "$1: deyroute-hub crashed or restarted (MainPID $hub_pid -> $h)"
  [ "$n" = "$node_pid" ] || fail "$1: deyroute-node crashed or restarted (MainPID $node_pid -> $n)"
}
# settled T: the engine of T is not in SWITCHING.
settled() { local s; s=$(tunnel_field "$1" .state) && [ -n "$s" ] && [ "$s" != SWITCHING ]; }
# no_hang PART FILE: no sample of FILE timed out.
no_hang() {
  local n
  n=$(count HANG "$2")
  [ "$n" = 0 ] || fail "$1: tunnel show did not answer within 20 s ($n times)"
}

setup_pair client
ct_acct # before the tunnels start, so that their flows are counted from the first packet
vpn_up node1 443 64
T=$(add_tunnel "$NODE1" 443)
wait_tunnel_up "$T" 180
hub_pid=$(main_pid hub deyroute-hub) node_pid=$(main_pid node1 deyroute-node)
{ [ "${hub_pid:-0}" -gt 0 ] && [ "${node_pid:-0}" -gt 0 ]; } || fail "deyroute-hub or deyroute-node is not running"
note "S35 on ${DEY_IMAGE:-?}, deyroute $DEY_VERSION, $(date -u '+%F %T') UTC; flows cut after $CUT packets"

# (a) The active rung of the default ladder is cut while the client works.
from=$(active_transport "$T")
port=$(tunnel_field "$T" '.rungs[] | select(.active) | .control_port')
[ "${port:-0}" -gt 0 ] || fail "(a): the active rung $from has no control port"
seq0=$(last_seq "$T")
start_prober 443
dl_end=$(($(date +%s%3N) + (WINDOW + 5) * 1000))
dl_start $((WINDOW + 5))
sleep 3
t0=$(date +%s%3N)
block --node "$NODE1_IP" --cut-after "$CUT" tcp "$port"
block --node "$NODE1_IP" --cut-after "$CUT" udp "$port"
states=$ART/S35-a-states.txt
watch_states "$T" "$WINDOW" "$states"
outage=$(stop_prober)
switches=$(switches_after "$T" "$seq0")
nsw=$(grep -c . <<<"$switches" || true)
first=none back="no switch"
if [ "$nsw" -gt 0 ]; then
  first=$(awk -v t0="$t0" 'NR == 1 { printf "%.1f s (%s -> %s)", ($1 - t0) / 1000, $2, $3 }' <<<"$switches")
  r=$(recovery_ms "$T")
  if [ "$r" -ge 0 ]; then back="back ${r} ms after the last switch"; else back="not back after the last switch"; fi
fi
wait_for 30 "(a): the client's downloads end" sh_on client "! systemctl is-active --quiet it-s35-dl"
note "(a) default ladder: $from cut on control port $port (tcp and udp) for ${WINDOW} s"
note "(a) time to the first switch: $first"
note "(a) switches: $nsw"
note "(a) states: $(sequence "$states")"
note "(a) longest SWITCHING: $(longest SWITCHING "$states") s, longest DEGRADED: $(longest DEGRADED "$states") s"
note "(a) events: $(events_after "$T" "$seq0")"
note "(a) client: longest outage ${outage} ms, $(probes), $back"
note "(a) client downloads at 256 kB/s: $(dl_summary "$dl_end")"
no_hang "(a)" "$states"
wait_for 120 "(a): the engine leaves SWITCHING (the cut is still on)" settled "$T"
alive "(a)"
unblock

# (b) test-ladder with another rung's control port cut.
dey tunnel reset "$T" --json >/dev/null # clear the quarantine of (a)
wait_tunnel_up "$T" 180
cut_tr="" cut_port=""
read -r cut_tr cut_port < <(tunnel_json "$T" | jq -r --arg n "$NODE1" '[.rungs[] |
  select(.node == $n and (.active | not) and (.skipped // "") == "" and .control_port > 0)][0] // empty |
  "\(.transport) \(.control_port)"') || true
[ -n "$cut_tr" ] || fail "(b): no other rung of $T has a control port to cut"
block --node "$NODE1_IP" --cut-after "$CUT" tcp "$cut_port"
block --node "$NODE1_IP" --cut-after "$CUT" udp "$cut_port"
rc=0
res=$(on hub timeout 1200 deyroute tunnel test-ladder "$T" --yes --json) || rc=$?
[ "$rc" != 124 ] || fail "(b): test-ladder did not finish within 20 minutes"
[ "$rc" = 0 ] || { printf '%s\n' "$res" >&2; fail "(b): test-ladder failed (exit $rc)"; }
printf '%s\n' "$res" >"$ART/S35-b-test-ladder.json"
note "(b) test-ladder with $cut_tr cut on control port $cut_port (tcp and udp):"
while read -r line; do note "(b)   $line"; done < <(jq -r '.results[] | "\(.transport) on \(.node): " +
  (if (.skipped // "") != "" then "skipped" elif .ok then "ok \(.rtt_ms) ms" else "failed" end) +
  (if .error.code then " \(.error.code)" else "" end)' <<<"$res")
code=$(jq -r --arg n "$NODE1" --arg tr "$cut_tr" \
  '.results[] | select(.node == $n and .transport == $tr) | .error.code // empty' <<<"$res")
others=$(jq -r --arg n "$NODE1" --arg tr "$cut_tr" '[.results[] |
  select((.node != $n or .transport != $tr) and (.skipped // "") == "" and (.ok | not)) |
  "\(.transport) \(.error.code // "?")"] | join(", ")' <<<"$res")
note "(b) other rungs not OK: ${others:-none}"
unblock
wait_for 120 "(b): the engine leaves SWITCHING after test-ladder" settled "$T"
alive "(b)"
if [ "$code" = DEY-B073 ]; then
  note "(b) $cut_tr reports DEY-B073: passed the probe, then failed"
elif [ -z "$code" ]; then
  note "(b) $cut_tr reports no error code: DEY-B073 (passed the probe, then failed) is absent"
elif on hub grep -qa DEY-B073 /usr/local/bin/deyroute; then
  fail "(b): $cut_tr reports $code, want DEY-B073 (passed the probe, then failed)"
else
  note "(b) $cut_tr reports $code; this build has no DEY-B073"
fi

# (c) A UDP-only tunnel with its active rung's UDP control port cut.
udp_echo node1 27015
U=$(add_tunnel "$NODE1" 27015/udp --name s35-udp)
wait_tunnel_up "$U" 180
echo_ok() { [ "$(udp_ask "$HUB_IP" 27015)" = echo:it-ping ]; }
base=no
for _ in 1 2 3 4 5 6; do
  if echo_ok; then base=yes; break; fi
  sleep 1
done
utr=$(active_transport "$U")
uport=$(tunnel_field "$U" '.rungs[] | select(.active) | .control_port')
verdict="not measured"
if [ "${uport:-0}" -gt 0 ]; then
  useq=$(last_seq "$U")
  block --node "$NODE1_IP" --cut-after "$CUT" udp "$uport"
  ustates=$ART/S35-c-states.txt
  : >"$ustates"
  ok=0 bad=0 t0=$SECONDS
  while [ $((SECONDS - t0)) -lt 60 ]; do
    printf '%s %s\n' $((SECONDS - t0)) "$(sample "$U")" >>"$ustates"
    if echo_ok; then
      ok=$((ok + 1))
      sleep 1
    else
      bad=$((bad + 1)) # udp_ask waited 10 s for it
    fi
  done
  usw=$(switches_after "$U" "$useq" | grep -c . || true)
  note "(c) UDP-only tunnel on $utr (echo before the cut: $base): UDP control port $uport cut for 60 s"
  note "(c) states: $(sequence "$ustates")"
  note "(c) switches: $usw; events: $(events_after "$U" "$useq")"
  note "(c) echoes through the tunnel: $ok answered, $bad not"
  if [ "$usw" -gt 0 ]; then
    verdict="the engine switched away from the cut rung"
  elif [ "$bad" = 0 ]; then
    verdict="the cut did not stop the echoes"
  elif [ "$(count UP "$ustates")" = "$(awk 'END { print NR }' "$ustates")" ]; then
    verdict="blind spot: UP on $utr throughout while $bad of $((ok + bad)) echoes went unanswered (the probe of a UDP-only tunnel checks its units only)"
  else
    verdict="the state changed without a switch (see the states)"
  fi
  note "(c) $verdict"
  no_hang "(c)" "$ustates"
  wait_for 120 "(c): the engine leaves SWITCHING (the cut is still on)" settled "$U"
else
  note "(c) the active rung $utr of the UDP-only tunnel has no control port: not measured"
fi
alive "(c)"
unblock
pass "measured (${MEASURE##*/}): (a) first switch $first, $nsw switches, outage ${outage} ms; (b) $cut_tr ${code:-no code}; (c) $verdict"
