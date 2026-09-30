#!/usr/bin/env bash
# S30: ACME with a real domain (manual test, Let's Encrypt staging): issued, renewal simulated, fallback to auto
# This scenario needs a public domain pointing at a reachable hub and cannot run
# in the lab network. Manual procedure (spec section 17):
#   1. deyroute settings / config edit: hub.domain = <domain>, the tunnel's tls mode = acme
#      (DEYROUTE_ACME_STAGING=1 for the staging directory)
#   2. deyroute security tls show   -> the tunnel certificate is issued by (STAGING) Let's Encrypt
#   3. deyroute security tls renew --tunnel <id> -> renewed, the active candidate restarts once
#   4. point the domain elsewhere, renew again -> event acme_failed, the tunnel falls back to auto
# The fallback path (step 4) is covered by the hub unit tests.
# shellcheck source=../lib.sh
source "$(dirname "$0")/../lib.sh"
skip "manual scenario: needs a real domain (see the comment at the top)"
