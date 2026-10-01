#!/usr/bin/env bash
# S03: join with an expired token / a wrong fingerprint: DEY-N001 / DEY-N002, nothing registered
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

up hub node1 node2
install_hub hub ir-1 >&2
on node1 bash /dist/install.sh --skip-signature --local "$ARCHIVE" --no-setup >&2
on node2 bash /dist/install.sh --skip-signature --local "$ARCHIVE" --no-setup >&2

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

# A token is single-use: the valid link works once.
dey --on node1 join "$link" --yes >&2 || fail "join with the valid link failed"
expect_code DEY-N001 dey --on node2 join "$link" --yes >&2
[ "$(dey node list --json | jq '.nodes | length')" = 1 ] || fail "the reused token registered a second node"
pass
