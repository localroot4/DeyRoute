#!/usr/bin/env bash
# S24: 80-column terminal, TERM=dumb, no UTF-8: the menu is usable, the banner is ASCII
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

up hub
install_hub hub ir-1 >&2
env_dumb=(-e TERM=dumb -e LANG=C -e LC_ALL=C -e COLUMNS=80 -e LINES=24)
out=$(docker exec "${env_dumb[@]}" "$(cid hub)" deyroute menu --once) || fail "menu --once failed"
printf '%s\n' "$out" >&2
LC_ALL=C grep -q '[^[:print:][:space:]]' <<<"$out" && fail "non-ASCII or control bytes in the dumb-terminal menu"
wide=$(awk 'length($0) > 80' <<<"$out" | head -n 3)
[ -z "$wide" ] || fail "lines wider than 80 columns: $wide"
for item in Dashboard Tunnels Nodes Ports Failover Diagnostics; do
  grep -q "$item" <<<"$out" || fail "menu item $item missing"
done

# Line mode drives the menu with numbers and q (a pipe instead of a terminal).
out=$(printf '1\nq\nq\n' | docker exec -i "${env_dumb[@]}" "$(cid hub)" deyroute 2>&1) || fail "line-mode menu exited non-zero: $out"
LC_ALL=C grep -q '[^[:print:][:space:]]' <<<"$out" && fail "non-ASCII bytes in line mode"
grep -q "1) Dashboard" <<<"$out" || fail "line mode printed no menu: $out"
grep -q "TUNNELS" <<<"$out" || fail "line mode did not answer 1 (dashboard): $out"
pass
