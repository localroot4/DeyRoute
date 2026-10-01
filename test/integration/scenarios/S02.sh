#!/usr/bin/env bash
# S02: re-running the installer on an installed system upgrades/repairs it; config untouched
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"

up hub
OLD=/dist/deyroute_${DEY_OLD_VERSION}_linux_${ARCH}.tar.gz
# The lab release is not signed: without --skip-signature nothing is installed.
out=$(on hub bash /dist/install.sh --local "$OLD" --no-setup 2>&1) && fail "an unsigned --local archive was installed"
grep -q DEY-I006 <<<"$out" || fail "unsigned --local: want DEY-I006, got: $out"
on hub test ! -e /usr/local/bin/deyroute || fail "the binary was installed despite DEY-I006"
on hub bash /dist/install.sh --skip-signature --local "$OLD" --role hub --name ir-1 --yes >&2 || fail "installing the old release failed"
[ "$(dey version --json | jq -r .version)" = "$DEY_OLD_VERSION" ] || fail "old version not installed"

cfg=$(on hub sha256sum /etc/deyroute/config.yaml)
sec=$(sh_on hub "cd /etc/deyroute/secrets && find . -type f | sort | xargs sha256sum")
on hub rm -f /usr/local/bin/dey /etc/systemd/system/deyroute-hub.service # damage the installation

on hub bash /dist/install.sh --skip-signature --local "$ARCHIVE" >&2 || fail "the upgrade run failed"
[ "$(dey version --json | jq -r .version)" = "$DEY_VERSION" ] || fail "not upgraded"
[ "$(on hub readlink -f /usr/local/bin/dey)" = /usr/local/bin/deyroute ] || fail "dey link not repaired"
[ "$(on hub sha256sum /etc/deyroute/config.yaml)" = "$cfg" ] || fail "config.yaml changed"
[ "$(sh_on hub "cd /etc/deyroute/secrets && find . -type f | sort | xargs sha256sum")" = "$sec" ] || fail "secrets changed"
wait_for 30 "deyroute-hub active" on hub systemctl is-active --quiet deyroute-hub.service

# Setup flags on an installed system are ignored (no second setup).
on hub bash /dist/install.sh --skip-signature --local "$ARCHIVE" --role hub --name other --yes >&2 || fail "the repair run failed"
[ "$(on hub sha256sum /etc/deyroute/config.yaml)" = "$cfg" ] || fail "config.yaml changed by the repair run"
wait_for 30 "deyroute-hub active" on hub systemctl is-active --quiet deyroute-hub.service
dey status --json | jq -e '.role == "hub" and .hub.name == "ir-1"' >/dev/null || fail "status after repair"
pass
