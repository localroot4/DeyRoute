#!/usr/bin/env bash
# Refuses release binaries that trust the integration lab's signing key
# (DR-03). A lab build trusts a second, throwaway minisign key next to the
# release key (internal/install.TestPublicKey, set only with
# -ldflags -X); a release binary must not. Each BINARY must answer
# `--json version` with "test_key": false.
#
# A binary for the other CPU runs through qemu-user: qemu-<cpu>-static or
# qemu-<cpu> on PATH (Ubuntu: apt-get install qemu-user-static), else the
# kernel's binfmt handler. A binary that does not run, does not answer with
# JSON or does not say false fails the check: nothing unchecked is released.
#
# Run by the edge build (.github/workflows/release-edge.yml) and by
# goreleaser for each stable release binary (.goreleaser.yaml, build hook);
# tested by test/release/edge_test.sh. Needs bash, od and jq.
#
#   bash scripts/no-test-key.sh dist/deyroute_*_linux_*/deyroute
set -euo pipefail

fail() {
  echo "no-test-key: $*" >&2
  exit 1
}
[ $# -gt 0 ] || {
  echo "usage: no-test-key.sh BINARY..." >&2
  exit 2
}
command -v jq >/dev/null 2>&1 || fail "jq is required"

# elf_cpu FILE: the CPU of the ELF executable FILE as uname -m names it
# (x86_64, aarch64); nothing for any other file.
elf_cpu() {
  [ "$(head -c 4 "$1" | od -An -tx1 | tr -d ' \n')" = 7f454c46 ] || return 0
  case $(od -An -tu2 -j 18 -N 2 "$1" | tr -d ' \n') in
    62) echo x86_64 ;;
    183) echo aarch64 ;;
  esac
}

err=$(mktemp)
trap 'rm -f "$err"' EXIT
for bin in "$@"; do
  [ -f "$bin" ] || fail "$bin: no such file"
  cpu=$(elf_cpu "$bin")
  [ -n "$cpu" ] || fail "$bin: not an x86_64 or aarch64 ELF binary"
  run=("$bin")
  if [ "$cpu" != "$(uname -m)" ]; then
    for q in "qemu-$cpu-static" "qemu-$cpu"; do
      if command -v "$q" >/dev/null 2>&1; then
        run=("$q" "$bin")
        break
      fi
    done
  fi
  out=$("${run[@]}" --json version 2>"$err") ||
    fail "$bin: '${run[*]} --json version' failed, so it cannot be checked (an $cpu binary on $(uname -m) needs qemu-user-static): $(cat "$err")"
  key=$(jq -r 'if type == "object" and has("test_key") then .test_key | tostring else "absent" end' <<<"$out" 2>/dev/null) ||
    key=unreadable
  [ "$key" = false ] ||
    fail "$bin: test_key is $key in its --json version: a release binary must not trust the lab signing key (build it without -X …/internal/install.TestPublicKey)"
  echo "no-test-key: $bin ($cpu, version $(jq -r '.version // "?"' <<<"$out")): test_key false"
done
