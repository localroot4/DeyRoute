#!/usr/bin/env bash
# S05: port 443 already taken by nginx: DEY-P012 naming nginx, free ports suggested
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

setup_pair
# A stand-in "nginx" (a python3 copy with that name) listening on 443.
sh_on hub "cp \"\$(readlink -f /usr/bin/python3)\" /usr/local/sbin/nginx && mkdir -p /srv/www &&
  systemd-run --quiet --unit fake-nginx -p WorkingDirectory=/srv/www /usr/local/sbin/nginx -m http.server 443"
wait_for 20 "fake nginx listening" sh_on hub "ss -Hltn 'sport = :443' | grep -q ."

out=$(expect_code DEY-P012 dey tunnel add --node "$NODE1" --ports 443 --ladder "$(it_ladder)" --yes)
grep -q nginx <<<"$out" || fail "DEY-P012 does not name nginx: $out"

pc=$(dey port check 443 --json) || true
jq -e '.bind_free == false and (.bind_process | test("nginx"))' <<<"$pc" >/dev/null || fail "port check: $pc"
jq -e '.suggested_ports | length > 0' <<<"$pc" >/dev/null || fail "no suggested ports: $pc"
[ "$(dey tunnel list --json | jq '.tunnels | length')" = 0 ] || fail "a tunnel was created on a busy port"
pass
