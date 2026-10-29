#!/usr/bin/env bash
# Release notes of the stable release v<VERSION>, which the release job of
# ci.yml passes to goreleaser (--release-notes): how to install and update,
# the first six sections of "## [VERSION]" in CHANGELOG.md, links and the
# key that signs SHA256SUMS. It fails when CHANGELOG.md has no such section,
# so a tag without its changelog publishes nothing.
#
#   bash scripts/release-notes.sh 0.3.0 > notes.md
set -euo pipefail

ver=${1:?usage: release-notes.sh VERSION}
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
repo="https://github.com/${GITHUB_REPOSITORY:-localroot4/DeyRoute}"
key=$(bash "$root/scripts/release-pubkey.sh")
# One awk reading the file: an early exit in a pipe is SIGPIPE under
# pipefail.
news=$(awk -v h="## [$ver]" '
  index($0, h) == 1 && (length($0) == length(h) || substr($0, length(h) + 1, 1) == " ") { on = 1; next }
  on && /^## \[/ { exit }
  on {
    if (!seen && /^$/) next
    seen = 1
    if (/^### /) n++
    if (n > 6) { more = 1; exit }
    print
  }
  END { if (more) print "\nMore in the full changelog." }' "$root/CHANGELOG.md")
[ -n "$news" ] || {
  echo "release-notes: CHANGELOG.md has no section [$ver]" >&2
  exit 1
}

echo '**Install on the hub (Iran server)** — نصب روی هاب (سرور ایران):'
echo
echo '```bash'
echo "bash <(curl -fsSL ${repo}/releases/latest/download/install.sh)"
echo '```'
echo
echo "This release exactly: \`bash <(curl -fsSL ${repo}/releases/download/v${ver}/install.sh) --version ${ver}\`"
echo
echo "Already installed? Run \`deyroute update\` on the hub (the nodes follow)."
echo "قبلاً نصب کرده‌اید؟ روی هاب \`deyroute update\` بزنید (نودها خودشان دنبال می‌کنند)."
echo
echo "## What's new in ${ver}"
echo
printf '%s\n' "$news"
echo
echo "[Full changelog](${repo}/blob/v${ver}/CHANGELOG.md) · [راهنمای فارسی](${repo}/blob/v${ver}/docs/fa/index.md) · [English guides](${repo}/blob/v${ver}/docs/en/index.md)"
echo
echo '## Verify'
echo
echo "\`SHA256SUMS\` is signed with minisign; the installer and \`deyroute update\` check it before anything is installed. Public key: \`${key}\`"
