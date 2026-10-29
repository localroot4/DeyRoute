#!/usr/bin/env bash
# S03: join with an expired token / a wrong fingerprint: DEY-N001 / DEY-N002, nothing registered; a revoked token: DEY-N001 and the control port is restricted to @nodes again; a gone --version: DEY-I024
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

up hub node1 node2
install_hub hub ir-1 >&2
on node1 bash /dist/install.sh --local "$ARCHIVE" --no-setup >&2
on node2 bash /dist/install.sh --local "$ARCHIVE" --no-setup >&2

# Wrong CA fingerprint: refused before the token is sent.
link=$(join_link)
bad="${link%%#*}#sha256:$(printf '0%.0s' $(seq 64))"
expect_code DEY-N002 dey --on node1 join "$bad" --yes >&2

# Expired token (the shortest TTL is one minute).
expired=$(dey node join-command --ttl 1m --json | jq -r .link)
sleep 62
expect_code DEY-N001 dey --on node1 join "$expired" --yes >&2

registered=$(dey node list --json | jq '.nodes | length')
[ "$registered" = 0 ] || fail "$registered node(s) registered after failed joins"
on node1 test ! -e /etc/deyroute/config.yaml || fail "a failed join left a config on the node"

# A token is single-use: the valid link works once. The join itself (phase 2:
# < 30s) is timed up to the node being online on the hub.
j0=$(date +%s%3N)
dey --on node1 join "$link" --yes >&2 || fail "join with the valid link failed"
wait_node_online "$(node_id_of node1)" 30
jms=$(($(date +%s%3N) - j0))
log "deyroute join: node online after ${jms}ms"
[ "$jms" -lt 30000 ] || fail "join took ${jms}ms (limit 30s)"
expect_code DEY-N001 dey --on node2 join "$link" --yes >&2
[ "$(dey node list --json | jq '.nodes | length')" = 1 ] || fail "the reused token registered a second node"

# Join-token revoke (DR-16): a new join command opens the join window (the
# control port accepts every source); node join-revoke deletes the token and
# applies the firewall before it returns, so the port is restricted to
# @nodes again at once, and the revoked link fails with DEY-N001.
cport=$(dey status --json | jq -r .hub.control_port)
ctl_rules() { on hub nft list table inet deyroute | grep -E "tcp dport $cport( |$)"; }
revoked_link=$(join_link)
ctl_rules | grep -qE "tcp dport $cport accept" || fail "a join command did not open the join window:"$'\n'"$(ctl_rules)"
rv=$(dey node join-revoke --json)
jq -e '.schema == 1 and .ok == true and .revoked == 1' <<<"$rv" >/dev/null || fail "join-revoke: $rv"
rules=$(ctl_rules)
grep -q "ip saddr @nodes accept" <<<"$rules" || fail "the control port is not restricted to @nodes after join-revoke:"$'\n'"$rules"
grep -qE "tcp dport $cport drop" <<<"$rules" || fail "unknown sources are not dropped after join-revoke:"$'\n'"$rules"
if grep -qE "tcp dport $cport accept" <<<"$rules"; then fail "the join window is still open after join-revoke:"$'\n'"$rules"; fi
has_event join_tokens_revoked || fail "no join_tokens_revoked event"
expect_code DEY-N001 dey --on node2 join "$revoked_link" --yes >&2
[ "$(dey node list --json | jq '.nodes | length')" = 1 ] || fail "the revoked token registered a node"
[ "$(dey node join-revoke --json | jq .revoked)" = 0 ] || fail "a second join-revoke found a token"

# A pinned release that is gone (DR-02): when every source answers HTTP 404
# for SHA256SUMS of --version V, the installer stops at once with DEY-I024
# (no retry). Without --version, or when a source fails otherwise (retried),
# it stays DEY-I004. A local server stands in for GitHub in a lab copy of the
# installer, so the step needs no internet. Nothing is installed.
sh_on node2 "mkdir -p /srv/it-gone && sed 's|^GITHUB_BASE=.*|GITHUB_BASE=\"http://127.0.0.1:8099/gh\"|' /dist/install.sh >/tmp/install-gone.sh"
it_unit node2 gone "/usr/bin/python3 -m http.server 8099 --bind 127.0.0.1 --directory /srv/it-gone"
wait_for 20 "404 server on node2" sh_on node2 "ss -Hltn 'sport = :8099' | grep -q ."
install_gone() { on node2 bash /tmp/install-gone.sh --no-setup "$@"; }
t0=$SECONDS
out=$(expect_code DEY-I024 install_gone --version 9.9.9 --mirror http://127.0.0.1:8099/mirror)
grep -q 'Release v9.9.9 not found' <<<"$out" || fail "DEY-I024 does not name the version:"$'\n'"$out"
if grep -q 'retry in' <<<"$out"; then fail "a 404 was retried:"$'\n'"$out"; fi
[ $((SECONDS - t0)) -lt 5 ] || fail "DEY-I024 took $((SECONDS - t0))s: a 404 must not wait for retries"
out=$(expect_code DEY-I004 install_gone --mirror http://127.0.0.1:8099/mirror)
if grep -q DEY-I024 <<<"$out"; then fail "DEY-I024 without --version:"$'\n'"$out"; fi
out=$(expect_code DEY-I004 install_gone --version 9.9.9 --mirror http://127.0.0.1:1)
grep -q 'retry in' <<<"$out" || fail "an unreachable mirror was not retried:"$'\n'"$out"
on node2 deyroute version >/dev/null || fail "the failed installs touched the installed binary"
pass "join ${jms}ms"
