#!/usr/bin/env bash
# S35: passes, then cut: flows of a rung's control port die after 12 packets; switch time, states, switches and client outage measured
# A measurement, not an acceptance test (the filtering report from Iran of
# 2026-10-17): no rung is asserted to get through such a filter. The cut is
# lib.sh block --cut-after on the hub: a flow to or from the port is dropped
# once conntrack has counted 12 packets in its original direction, and
# every new flow passes again for 12 packets. Accounting is on before the
# tunnels start, so the flows they already have are cut as well. The rules
# sit in the hub's input and output hooks, so what the hub itself sends on a
# cut flow is refused locally (EPERM for UDP), not lost on the path. Each
# part records how many packets the cut dropped; with none it measured
# nothing and says so.
#  (a) The default ladder is UP and its active rung's control port is cut;
#      for 120 s the Xray client downloads an 8 MiB file at 256 KiB/s (about
#      32 s each, one after another; a download that moves less than 1 KiB/s
#      for 10 s is given up) and the prober requests every 200 ms. Recorded:
#      the time to the first switch (or none), the state sequence, the number
#      of switches, the client's longest outage and downloads (only when a
#      prober request got through before the cut).
#  (b) test-ladder with another rung's control port cut. Expected: that rung
#      reports DEY-B073 (passed the probe, then failed). Any other result is
#      logged, not failed: another code (the rung did not pass its first
#      probe within 12 packets a flow, or the build has no DEY-B073), a rung
#      that passed (the cut did not stop it in its window) or no code.
#  (c) A UDP-only tunnel with its active rung's UDP control port cut: its
#      probe checks its units only, so it is expected to stay UP while the
#      echoes through it go unanswered (the blind spot, recorded; not
#      measured when no echo got through before the cut). A backend that
#      gives up on the EPERM of the hub's own sends shows in the events.
# S35 fails only when:
#  - the lab cannot be set up: a setup helper fails, or a tunnel is not UP
#    within 180 s without the filter (at the start of (a) and (c), and after
#    the reset that ends (a));
#  - the CLI hangs (tunnel show 20 s, other calls 30 s, tunnel reset 120 s,
#    test-ladder 20 min), or a call other than the state samples and tunnel
#    reset (whose failure is logged) exits with an error;
#  - test-ladder fails (it aborted or found the engine stopped);
#  - a daemon crashes or restarts;
#  - the engine stays in SWITCHING for 120 s after a part.
# The numbers go to $ART/S35-measure.txt, the state samples to
# $ART/S35-a-states.txt and $ART/S35-c-states.txt, the test-ladder result to
# $ART/S35-b-test-ladder.json.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
needs_online "a ladder with more than one rung"

CUT=${S35_CUT_AFTER:-12} WINDOW=${S35_WINDOW_S:-120}
MEASURE=$ART/S35-measure.txt
: >"$MEASURE"
# note LINE: one line of the measurement, to the log and to $MEASURE.
note() { log "$*"; printf '%s\n' "$*" >>"$MEASURE"; }

# dey_in SECONDS ARGS...: deyroute ARGS on the hub; fails the scenario (a
# CLI hang) when it does not answer within SECONDS. Every CLI call of S35
# after the setup goes through it or through sample. In a command
# substitution or a pipeline the failure ends only that subshell, so the
# helpers below pass its status on and S35 calls them as VAR=$(...) at the
# top level, where set -e stops the scenario.
dey_in() {
  local t=$1 rc=0
  shift
  on hub timeout "$t" deyroute "$@" || rc=$?
  [ "$rc" != 124 ] || fail "CLI hang: deyroute $* did not answer within $t s"
  return "$rc"
}
# tjson T / tfield T FILTER: tunnel show of T (tunnel_json and tunnel_field
# with a time limit).
tjson() { dey_in 30 tunnel show "$1" --json; }
tfield() { local j; j=$(tjson "$1") || return; jq -r "$2" <<<"$j"; }

# sample T: "STATE TRANSPORT" of tunnel T now; "HANG -" when tunnel show
# does not answer within 20 s, "ERR -" when it fails.
sample() {
  local out rc=0
  out=$(on hub timeout 20 deyroute tunnel show "$1" --json 2>/dev/null) || rc=$?
  if [ "$rc" = 124 ]; then echo "HANG -"; return; fi
  if [ "$rc" != 0 ] || [ -z "$out" ]; then echo "ERR -"; return; fi
  jq -r '"\(.state) \((.active_transport | select(. != null and . != "")) // "-")"' <<<"$out" 2>/dev/null || echo "ERR -"
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
# no_hang PART FILE: no sample of FILE timed out.
no_hang() {
  local n
  n=$(count HANG "$2")
  [ "$n" = 0 ] || fail "$1: CLI hang: tunnel show did not answer within 20 s ($n times)"
}
# wait_up PART T SECONDS: tunnel T is UP within SECONDS (no filter is on).
wait_up() {
  local end=$((SECONDS + $3)) s
  while :; do
    s=$(sample "$2")
    s=${s%% *}
    case $s in
      UP) return 0 ;;
      HANG) fail "$1: CLI hang: tunnel show $2 did not answer within 20 s" ;;
    esac
    [ "$SECONDS" -lt "$end" ] || fail "$1: tunnel $2 is not UP after $3 s without the filter (state $s)"
    sleep 1
  done
}
# leave_switching PART T: the engine of T leaves SWITCHING within 120 s.
leave_switching() {
  local end=$((SECONDS + 120)) s
  while :; do
    s=$(sample "$2")
    s=${s%% *}
    case $s in
      HANG) fail "$1: CLI hang: tunnel show $2 did not answer within 20 s" ;;
      SWITCHING | ERR) ;;
      *) return 0 ;;
    esac
    [ "$SECONDS" -lt "$end" ] || fail "$1: the engine of $2 did not leave SWITCHING within 120 s (last sample: $s)"
    sleep 1
  done
}

# events_of T: tunnel T's events of the last 2 hours (JSON).
events_of() { dey_in 30 events --since 2h --json --tunnel "$1"; }
# last_seq T: the number of tunnel T's newest event (0: none).
last_seq() { local j; j=$(events_of "$1") || return; jq '[.events[].seq] | max // 0' <<<"$j"; }
# events_after T SEQ: the types of T's events after event SEQ, oldest first.
events_after() {
  local j
  j=$(events_of "$1") || return
  jq -r --argjson s "$2" '[.events | sort_by(.seq)[] | select(.seq > $s) | .type] | join(", ")' <<<"$j"
}
# switches_after T SEQ: T's switch_transport and switch_node events after
# event SEQ, oldest first, one per line: epoch-ms, from, to.
switches_after() {
  local j at from to
  j=$(events_of "$1") || return
  jq -r --argjson s "$2" '.events | sort_by(.seq)[] | select(.seq > $s) |
    select(.type == "switch_transport" or .type == "switch_node") |
    "\(.at) \(.from_transport // "-") \(.to_transport // "-")"' <<<"$j" |
    while read -r at from to; do echo "$(date -d "$at" +%s%3N) $from $to"; done
}

# dl_start SECONDS: the client downloads /blob through the VPN (vpn_up) at
# 256 KiB/s, one download after another, for SECONDS; a download that moves
# less than 1 KiB/s for 10 s is given up (curl exit 28). One line per
# download in /tmp/s35-dl.log: start and end (epoch-ms), curl's exit code
# ("end" for one that was still running when SECONDS ended), bytes.
dl_start() {
  on client sh -c 'cat > /usr/local/bin/it-s35-dl && chmod 0755 /usr/local/bin/it-s35-dl' <<'SH'
#!/bin/bash
end=$(($(date +%s) + $1)) url=$2
shift 2
while left=$((end - $(date +%s))) && [ "$left" -gt 0 ]; do
  s=$(date +%s%3N)
  n=$(curl -sS -o /dev/null --limit-rate 256k --speed-limit 1024 --speed-time 10 --max-time "$left" \
    -w '%{size_download}' "$@" "$url" 2>/dev/null)
  rc=$?
  [ "$rc" != 28 ] || [ "$(date +%s)" -lt "$end" ] || rc=end
  echo "$s $(date +%s%3N) $rc ${n:-0}" >>/tmp/s35-dl.log
  [ "$rc" = 0 ] || sleep 1
done
SH
  sh_on client "systemctl stop it-s35-dl 2>/dev/null; rm -f /tmp/s35-dl.log
    systemd-run --quiet --unit it-s35-dl -E NO_PROXY= -E no_proxy= /usr/local/bin/it-s35-dl $1 $VPN_URL/blob ${VPN_CURL:-}"
}
# dl_stop: the downloads of dl_start end (given 30 s, then stopped).
dl_stop() {
  local end=$((SECONDS + 30))
  while sh_on client "systemctl is-active --quiet it-s35-dl" && [ "$SECONDS" -lt "$end" ]; do sleep 1; done
  sh_on client "systemctl stop it-s35-dl 2>/dev/null || true"
}
# dl_summary: the downloads of dl_start.
dl_summary() {
  { on client cat /tmp/s35-dl.log 2>/dev/null || true; } | awk '
    { b += $4; if ($3 == "0") ok++; else if ($3 == "end") run++; else cut++ }
    END { printf "%d downloads: %d complete, %d interrupted, %d still running at the end; %.1f MiB in total\n", NR, ok, cut, run, b / 1048576 }'
}
# prober_through SECONDS: a request of the prober got through within SECONDS.
prober_through() {
  local end=$((SECONDS + $1))
  until on client grep -q ' ok$' /tmp/prober.log 2>/dev/null; do
    [ "$SECONDS" -lt "$end" ] || return 1
    sleep 1
  done
}
# probes: how many of the prober's requests failed.
probes() {
  { on client cat /tmp/prober.log 2>/dev/null || true; } |
    awk '$2 == "fail" { f++ } END { printf "%d of %d prober requests failed\n", f, NR }'
}
# back_after MS: milliseconds from MS (epoch-ms) to the prober's first good
# request after it; -1 when there is none.
back_after() {
  { on client cat /tmp/prober.log 2>/dev/null || true; } | awk -v t1="$1" '
    $1 >= t1 && $2 == "ok" { print $1 - t1; found = 1; exit }
    END { if (!found) print -1 }'
}

# alive PART: neither daemon crashed or restarted since the start.
alive() {
  local h n
  h=$(main_pid hub deyroute-hub) n=$(main_pid node1 deyroute-node)
  [ "$h" = "$hub_pid" ] || fail "$1: deyroute-hub crashed or restarted (MainPID $hub_pid -> $h)"
  [ "$n" = "$node_pid" ] || fail "$1: deyroute-node crashed or restarted (MainPID $node_pid -> $n)"
}

setup_pair client
ct_acct # before the tunnels start, so that their flows are counted from the first packet
vpn_up node1 443 8
T=$(add_tunnel "$NODE1" 443)
wait_up setup "$T" 180
hub_pid=$(main_pid hub deyroute-hub) node_pid=$(main_pid node1 deyroute-node)
{ [ "${hub_pid:-0}" -gt 0 ] && [ "${node_pid:-0}" -gt 0 ]; } || fail "deyroute-hub or deyroute-node is not running"
note "S35 on ${DEY_IMAGE:-?}, deyroute $DEY_VERSION, $(date -u '+%F %T') UTC; flows cut after $CUT packets"

# (a) The active rung of the default ladder is cut while the client works.
asum="not measured"
from=$(tfield "$T" .active_transport)
port=$(tfield "$T" '[.rungs[] | select(.active) | .control_port // 0][0] // 0')
if [ "$port" -gt 0 ]; then
  seq0=$(last_seq "$T")
  start_prober 443
  pre=yes
  prober_through 30 || pre=no
  dl_start $((WINDOW + 5))
  sleep 3
  t0=$(date +%s%3N)
  block --node "$NODE1_IP" --cut-after "$CUT" tcp "$port"
  block --node "$NODE1_IP" --cut-after "$CUT" udp "$port"
  states=$ART/S35-a-states.txt
  watch_states "$T" "$WINDOW" "$states"
  no_hang "(a)" "$states"
  drops=$(cut_drops)
  outage=$(stop_prober)
  dl_stop
  switches=$(switches_after "$T" "$seq0")
  events=$(events_after "$T" "$seq0")
  nsw=$(grep -c . <<<"$switches" || true)
  first=none back="no switch"
  if [ "$nsw" -gt 0 ]; then
    first=$(awk -v t0="$t0" 'NR == 1 { printf "%.1f s (%s -> %s)", ($1 - t0) / 1000, $2, $3 }' <<<"$switches")
    last=$(tail -n 1 <<<"$switches")
    r=$(back_after "${last%% *}")
    if [ "$r" -ge 0 ]; then back="back ${r} ms after the last switch"; else back="not back after the last switch"; fi
  elif [ "$drops" = 0 ]; then
    first="not measured: the cut never engaged"
  fi
  note "(a) default ladder: $from cut on control port $port (tcp and udp) for ${WINDOW} s; the cut dropped $drops packets"
  note "(a) time to the first switch: $first"
  note "(a) switches: $nsw"
  note "(a) states: $(sequence "$states")"
  note "(a) longest SWITCHING: $(longest SWITCHING "$states") s, longest DEGRADED: $(longest DEGRADED "$states") s"
  note "(a) events: $events"
  if [ "$pre" = yes ]; then
    note "(a) client: longest outage ${outage} ms, $(probes), $back"
    note "(a) client downloads of 8 MiB at 256 KiB/s: $(dl_summary)"
    asum="first switch $first, $nsw switches, outage ${outage} ms"
  else
    note "(a) client: not measured: no prober request got through in 30 s before the cut"
    asum="first switch $first, $nsw switches, client not measured"
  fi
  leave_switching "(a)" "$T"
  alive "(a)"
  unblock
else
  note "(a) not measured: the active rung $from has no control port"
fi

# (b) test-ladder with another rung's control port cut.
bsum="not measured"
rc=0
dey_in 120 tunnel reset "$T" --json >/dev/null || rc=$? # clear the quarantine of (a)
[ "$rc" = 0 ] || note "(b) tunnel reset failed (exit $rc); test-ladder starts from the rung $T is on"
wait_up "(b)" "$T" 180
j=$(tjson "$T")
cut_tr="" cut_port=""
read -r cut_tr cut_port < <(jq -r --arg n "$NODE1" '[.rungs[] |
  select(.node == $n and (.active | not) and (.skipped // "") == "" and (.control_port // 0) > 0)][0] // empty |
  "\(.transport) \(.control_port)"' <<<"$j") || true
if [ -n "$cut_tr" ]; then
  block --node "$NODE1_IP" --cut-after "$CUT" tcp "$cut_port"
  block --node "$NODE1_IP" --cut-after "$CUT" udp "$cut_port"
  rc=0
  res=$(dey_in 1200 tunnel test-ladder "$T" --yes --json) || rc=$?
  [ "$rc" = 0 ] || { printf '%s\n' "$res" >&2; fail "(b): test-ladder failed (exit $rc)"; }
  bdrops=$(cut_drops)
  unblock
  printf '%s\n' "$res" >"$ART/S35-b-test-ladder.json"
  note "(b) test-ladder with $cut_tr cut on control port $cut_port (tcp and udp); the cut dropped $bdrops packets:"
  while read -r line; do note "(b)   $line"; done < <(jq -r '.results[] | "\(.transport) on \(.node): " +
    (if (.skipped // "") != "" then "skipped" elif .ok then "ok \(.rtt_ms) ms" else "failed" end) +
    (if .error.code then " \(.error.code)" else "" end)' <<<"$res")
  got="" code=""
  read -r got code < <(jq -r --arg n "$NODE1" --arg tr "$cut_tr" '[.results[] |
    select(.node == $n and .transport == $tr)][0] // empty |
    (if (.skipped // "") != "" then "skipped" elif .ok then "ok" else "failed" end) + " " + (.error.code // "")' <<<"$res") || true
  others=$(jq -r --arg n "$NODE1" --arg tr "$cut_tr" '[.results[] |
    select((.node != $n or .transport != $tr) and (.skipped // "") == "" and (.ok | not)) |
    "\(.transport) \(.error.code // "?")"] | join(", ")' <<<"$res")
  note "(b) other rungs not OK: ${others:-none}"
  if [ "$code" = DEY-B073 ]; then
    note "(b) $cut_tr reports DEY-B073: passed the probe, then failed"
  elif [ "$bdrops" = 0 ]; then
    note "(b) not measured: the cut never engaged ($cut_tr ${got:-has no result}${code:+ $code})"
  elif [ -n "$code" ]; then
    note "(b) $cut_tr reports $code, not DEY-B073: it did not pass its first probe within $CUT packets a flow, or this build has no DEY-B073"
  elif [ "$got" = ok ]; then
    note "(b) $cut_tr passed: the cut did not stop it within its window; DEY-B073 is absent"
  else
    note "(b) $cut_tr ${got:-has no result} without an error code: DEY-B073 is absent"
  fi
  bsum="$cut_tr ${code:-${got:-no result}}"
  leave_switching "(b)" "$T"
  alive "(b)"
else
  note "(b) not measured: no other rung of $T has a control port to cut"
fi

# (c) A UDP-only tunnel with its active rung's UDP control port cut.
udp_echo node1 27015
U=$(add_tunnel "$NODE1" 27015/udp --name s35-udp)
wait_up "(c)" "$U" 180
echo_ok() { [ "$(udp_ask "$HUB_IP" 27015)" = echo:it-ping ]; }
base=no
for _ in 1 2 3 4 5 6; do
  if echo_ok; then base=yes; break; fi
  sleep 1
done
utr=$(tfield "$U" .active_transport)
uport=$(tfield "$U" '[.rungs[] | select(.active) | .control_port // 0][0] // 0')
verdict="not measured"
if [ "$uport" -gt 0 ]; then
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
  no_hang "(c)" "$ustates"
  cdrops=$(cut_drops)
  uswitches=$(switches_after "$U" "$useq")
  uevents=$(events_after "$U" "$useq")
  usw=$(grep -c . <<<"$uswitches" || true)
  note "(c) UDP-only tunnel on $utr (echo before the cut: $base): UDP control port $uport cut for 60 s; the cut dropped $cdrops packets"
  note "(c) states: $(sequence "$ustates")"
  note "(c) switches: $usw; events: $uevents"
  note "(c) echoes through the tunnel: $ok answered, $bad not"
  if [ "$base" != yes ]; then
    verdict="not measured: no echo through $utr before the cut"
  elif [ "$cdrops" = 0 ]; then
    verdict="not measured: the cut never engaged"
  elif [ "$usw" -gt 0 ]; then
    verdict="the engine switched away from the cut rung (the events show whether a unit failed on the EPERM of the hub's own sends)"
  elif [ "$bad" = 0 ]; then
    verdict="the cut did not stop the echoes"
  elif [ "$(count UP "$ustates")" = "$(awk 'END { print NR }' "$ustates")" ]; then
    verdict="blind spot: UP on $utr throughout while $bad of $((ok + bad)) echoes went unanswered (the probe of a UDP-only tunnel checks its units only)"
  else
    verdict="the state changed without a switch (see the states)"
  fi
  note "(c) $verdict"
  leave_switching "(c)" "$U"
else
  note "(c) not measured: the active rung $utr of the UDP-only tunnel has no control port"
fi
alive "(c)"
unblock
pass "measured (${MEASURE##*/}): (a) $asum; (b) $bsum; (c) $verdict"
