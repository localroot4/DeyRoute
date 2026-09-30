#!/usr/bin/env bash
# DEYROUTE installer (spec section 5): installs /usr/local/bin/deyroute, then runs `deyroute setup`;
# running it again repairs/upgrades (config untouched). See --help, e.g.:
#   curl -fsSL https://raw.githubusercontent.com/localroot4/DeyRoute/main/installer/install.sh | sudo bash -s -- --role hub --name ir-1 --yes
# Sources in order: --mirror URL (or $DEYROUTE_MIRROR) -> RELEASE_BASE -> GitHub releases, each tried
# 3 times (backoff 2s, 4s); mirror layout <base>/latest/<file> and <base>/v<ver>/<file>. curl/wget
# honour https_proxy. Only DEYROUTE release files are downloaded; nothing is installed unless
# SHA256SUMS matches and its minisign signature verifies (unless --skip-signature). Without the
# minisign tool OpenSSL >= 3 checks it: legacy "Ed" signatures (minisign -S -l) directly and
# prehashed "ED" ones (minisign's default) via BLAKE2b-512.
set -Eeuo pipefail

# RELEASE_BASE: owner CDN tried before GitHub; empty = GitHub only.
RELEASE_BASE=""
GITHUB_BASE="https://github.com/localroot4/DeyRoute/releases"
# Release public key; must equal internal/install.MinisignPublicKey (a test enforces it).
# The secret key is the repository secret MINISIGN_SECRET_KEY (scripts/minisign keygen).
MINISIGN_PUBKEY="RWQtE2Hu1KwstgupEuPAWy+J2tiIgFVTIcReWI+Qh+yGMY3s2xWpyMuO"
BIN_DIR=/usr/local/bin
VERSION="" MIRROR="${DEYROUTE_MIRROR:-}" LOCAL="" NO_SETUP=0 SKIP_SIG=0
ROLE="" NAME="" YES=0 JOIN="" ARCH="" ARCHIVE="" TMP=""
say() { printf '==> %s\n' "$*"; }
warn() { printf '!   %s\n' "$*" >&2; }
have() { command -v "$1" >/dev/null 2>&1; }
# die CODE MESSAGE WHY FIX: the three-line DEY error of docs/ERRORS.md, then exit 1.
die() { printf '✖ %s  %s\n  Why:  %s\n  Fix:  %s\n' "$1" "$2" "$3" "$4" >&2; exit 1; }
# pkg APT [DNF] [PACMAN]: one-line install command for the detected package manager.
pkg() {
  if have apt-get; then echo "apt-get install -y $1"; elif have dnf; then echo "dnf install -y ${2:-$1}"
  elif have pacman; then echo "pacman -S --noconfirm ${3:-$1}"; else echo "install the package '$1'"; fi
}
usage() {
  cat <<'EOF'
Usage: install.sh [options]                  install (or repair/upgrade) deyroute, then run the setup wizard
       install.sh join 'dey://...' [--name N]  install and join a hub as a node
  --role hub|node --name N --yes  non-interactive setup      --version V      install release V (default: latest)
  --mirror URL      try this mirror first (or DEYROUTE_MIRROR)   --no-setup       install only (then: deyroute setup / restore)
  --local FILE      offline install from deyroute_<ver>_linux_<arch>.tar.gz (SHA256SUMS[.minisig] next to it are checked)
  --skip-signature  do not verify SHA256SUMS.minisig (testing only)   -h, --help  this help
EOF
}
need_val() { case "${2:-}" in "" | -*) echo "install.sh: $1 needs a value (see --help)" >&2; exit 1 ;; esac; }
parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --*=*) set -- "${1%%=*}" "${1#*=}" "${@:2}"; continue ;;
      join) need_val join "${2:-}"; JOIN=$2; shift ;;
      --role) need_val "$1" "${2:-}"; ROLE=$2; shift ;;
      --name) need_val "$1" "${2:-}"; NAME=$2; shift ;;
      --version) need_val "$1" "${2:-}"; VERSION=${2#v}; [ "$VERSION" != latest ] || VERSION=""; shift ;;
      --mirror) need_val "$1" "${2:-}"; MIRROR=$2; shift ;;
      --local) need_val "$1" "${2:-}"; LOCAL=$2; shift ;;
      --yes | -y) YES=1 ;; --no-setup) NO_SETUP=1 ;; --skip-signature) SKIP_SIG=1 ;;
      -h | --help) usage; exit 0 ;;
      *) echo "install.sh: unknown argument: $1 (see --help)" >&2; exit 1 ;;
    esac
    shift
  done
}

check_system() {
  local sv kr kmaj kmin
  [ "$(id -u)" -eq 0 ] || die DEY-I001 "This command must run as root" \
    "deyroute manages systemd units, nftables and files under /etc and /var/lib" "re-run with sudo, e.g.: sudo bash install.sh"
  case "$(uname -m)" in
    x86_64 | amd64) ARCH=amd64 ;;
    aarch64 | arm64) ARCH=arm64 ;;
    *) die DEY-I003 "Unsupported CPU architecture: $(uname -m)" \
      "release binaries exist only for linux/amd64 and linux/arm64" "use an amd64 or arm64 server" ;;
  esac
  { have systemctl && [ -d /run/systemd/system ]; } || die DEY-I002 "systemd was not found" \
    "deyroute runs its hub, node and tunnel processes as systemd units" "use a distribution with systemd 245 or newer (Ubuntu 22.04+, Debian 12+)"
  sv=$(systemctl --version 2>/dev/null | awk 'NR == 1 {print $2}') sv=${sv%%[!0-9]*}
  [ "${sv:-0}" -ge 245 ] || die DEY-I008 "systemd is too old (${sv:-unknown})" \
    "deyroute needs systemd 245 or newer for unit sandboxing options" "upgrade the distribution to a supported release"
  kr=$(uname -r) kmaj=${kr%%.*} kmin=${kr#*.} kmin=${kmin%%[!0-9]*}
  { [ "$kmaj" -gt 5 ] || { [ "$kmaj" -eq 5 ] && [ "${kmin:-0}" -ge 4 ]; }; } 2>/dev/null || die DEY-I009 \
    "Linux kernel is too old ($kr)" "deyroute needs kernel 5.4 or newer (BBR, nftables features)" "upgrade the kernel or the distribution"
  [ -n "$LOCAL" ] || have curl || have wget || die DEY-I007 "Neither curl nor wget is installed" \
    "the installer needs one of them to download release files" "$(pkg curl)"
  have ip || die DEY-I010 "iproute2 (the ip command) is missing" \
    "public IP detection and WireGuard transports use iproute2" "$(pkg iproute2 iproute)"
  have nft || have iptables || die DEY-I011 "Neither nftables nor iptables is installed" \
    "deyroute manages its own firewall table and needs one of them" "$(pkg nftables)"
  [ -s /etc/ssl/certs/ca-certificates.crt ] || [ -s /etc/pki/tls/certs/ca-bundle.crt ] || [ -s /etc/ssl/cert.pem ] ||
    die DEY-I012 "ca-certificates is missing" "HTTPS downloads cannot be verified without the system CA bundle" "$(pkg ca-certificates)"
}
# fail CODE [FILE]: download and verification failures.
fail() {
  case "$1" in
    DEY-I004) die DEY-I004 "Download failed from every source: $2" "the mirror, the release base and GitHub were each tried 3 times without success" \
      "pass --mirror URL, set DEYROUTE_MIRROR, or download the archive elsewhere and use --local /path/file.tar.gz" ;;
    DEY-I005) die DEY-I005 "Checksum mismatch for $2" "the downloaded file does not match SHA256SUMS; it is corrupt or was tampered with" \
      "retry; if it persists use another mirror or --local with a file you verified" ;;
    *) die DEY-I006 "Signature invalid for SHA256SUMS" "SHA256SUMS.minisig was not produced by the DEYROUTE release key" \
      "use the official mirror; only for testing, --skip-signature bypasses this check" ;;
  esac
}
# dl URL FILE: 3 tries with backoff 2s, 4s.
dl() {
  local try
  for try in 1 2 3; do
    if have curl; then curl -fsSL --connect-timeout 15 --max-time 900 -A deyroute-installer -o "$2" "$1" && return 0
    else wget -q --tries=1 --timeout=15 -U deyroute-installer -O "$2" "$1" && return 0; fi
    [ "$try" -eq 3 ] || { warn "download failed: $1 (retry in $((try * 2))s)"; sleep $((try * 2)); }
  done
  return 1
}
# url_for KIND BASE FILE
url_for() {
  if [ "$1" = github ]; then
    if [ -n "$VERSION" ]; then echo "$2/download/v$VERSION/$3"; else echo "$2/latest/download/$3"; fi
  elif [ -n "$VERSION" ]; then echo "$2/v$VERSION/$3"; else echo "$2/latest/$3"; fi
}
# sum_ok FILE SUMS: the sha256 of FILE equals its line in SUMS.
sum_ok() {
  local want got
  want=$(awk -v f="${1##*/}" '{n = $2; sub(/^\*/, "", n); sub(/^\.\//, "", n)} n == f {print tolower($1); exit}' "$2")
  got=$(sha256sum "$1" | awk '{print $1}')
  [ -n "$want" ] && [ "$want" = "$got" ]
}
ossl_ok() { openssl pkeyutl -verify -pubin -inkey "$TMP/sig/pub.der" -keyform DER -rawin -in "$1" -sigfile "$2" >/dev/null 2>&1; }

# sig_ok MSG SIG: SIG is a valid minisign signature of MSG by MINISIGN_PUBKEY.
sig_ok() {
  if have minisign; then minisign -V -q -m "$1" -x "$2" -P "$MINISIGN_PUBKEY" >/dev/null 2>&1; return; fi
  local d="$TMP/sig" ov; ov=$(openssl version 2>/dev/null | awk '{print $2}')
  [ "${ov%%.*}" -ge 3 ] 2>/dev/null || die DEY-I006 "Signature invalid for SHA256SUMS" \
    "it cannot be checked: neither minisign nor OpenSSL 3 is installed" "$(pkg minisign) or use --skip-signature"
  mkdir -p "$d"
  base64 -d <<<"$MINISIGN_PUBKEY" >"$d/pk" 2>/dev/null || return 1 # "Ed" | key id (8) | Ed25519 key (32)
  sed -n 2p "$2" | base64 -d >"$d/s" 2>/dev/null || return 1        # algorithm (2) | key id (8) | signature (64)
  { [ "$(wc -c <"$d/pk")" -eq 42 ] && [ "$(wc -c <"$d/s")" -eq 74 ]; } || return 1
  [ "$(head -c 10 "$d/pk" | tail -c 8 | od -An -tx1)" = "$(head -c 10 "$d/s" | tail -c 8 | od -An -tx1)" ] || return 1
  { printf '\x30\x2a\x30\x05\x06\x03\x2b\x65\x70\x03\x21\x00'; tail -c 32 "$d/pk"; } >"$d/pub.der" # Ed25519 SPKI
  tail -c 64 "$d/s" >"$d/sig"
  case "$(head -c 2 "$d/s")" in
    Ed) cp "$1" "$d/msg" ;;
    ED) openssl dgst -blake2b512 -binary "$1" >"$d/msg" 2>/dev/null || die DEY-I006 "Signature invalid for SHA256SUMS" \
      "the signature is prehashed and this OpenSSL lacks BLAKE2b-512" "$(pkg minisign) or use --skip-signature" ;;
    *) return 1 ;;
  esac
  ossl_ok "$d/msg" "$d/sig" || return 1
  # The trusted comment is signed as well: Ed25519(signature || comment).
  { cat "$d/sig"; sed -n 3p "$2" | sed 's/^trusted comment: //' | tr -d '\r\n'; } >"$d/tc"
  sed -n 4p "$2" | base64 -d >"$d/tcsig" 2>/dev/null || return 1
  ossl_ok "$d/tc" "$d/tcsig"
}

fetch_release() {
  local err=DEY-I004 s kind base name
  local -a srcs=()
  if [ -n "$MIRROR" ]; then srcs+=("mirror ${MIRROR%/}"); fi
  if [ -n "$RELEASE_BASE" ]; then srcs+=("base ${RELEASE_BASE%/}"); fi
  srcs+=("github $GITHUB_BASE")
  for s in "${srcs[@]}"; do
    kind=${s%% *} base=${s#* }
    say "downloading from $base"
    dl "$(url_for "$kind" "$base" SHA256SUMS)" "$TMP/SHA256SUMS" || continue
    if [ "$SKIP_SIG" = 1 ]; then warn "--skip-signature: the SHA256SUMS signature is NOT verified"
    else
      dl "$(url_for "$kind" "$base" SHA256SUMS.minisig)" "$TMP/SHA256SUMS.minisig" || continue
      sig_ok "$TMP/SHA256SUMS" "$TMP/SHA256SUMS.minisig" || { err=DEY-I006; warn "bad signature from $base"; continue; }
    fi
    name=$(awk -v a="$ARCH" -v v="${VERSION:-[^_]+}" '{n = $2; sub(/^\*/, "", n)}
      n ~ ("^deyroute_" v "_linux_" a "[.]tar[.]gz$") {print n; exit}' "$TMP/SHA256SUMS")
    [ -n "$name" ] || { warn "no linux/$ARCH archive listed at $base"; continue; }
    VERSION=${name#deyroute_} VERSION=${VERSION%%_linux_*} # pin the version found; "latest" may move meanwhile
    dl "$(url_for "$kind" "$base" "$name")" "$TMP/$name" || continue
    sum_ok "$TMP/$name" "$TMP/SHA256SUMS" || { err=DEY-I005; warn "checksum mismatch from $base"; continue; }
    ARCHIVE="$TMP/$name"; return 0
  done
  fail "$err" "deyroute_${VERSION:-latest}_linux_${ARCH}.tar.gz"
}
use_local() {
  local dir
  [ -f "$LOCAL" ] || die DEY-I004 "Download failed from every source: $LOCAL" \
    "the file given to --local does not exist" "pass the path of deyroute_<version>_linux_${ARCH}.tar.gz"
  dir=$(cd "$(dirname "$LOCAL")" && pwd) ARCHIVE="$dir/${LOCAL##*/}"
  if [ ! -f "$dir/SHA256SUMS" ]; then warn "no SHA256SUMS next to $LOCAL: the checksum is NOT verified"; return 0; fi
  if [ "$SKIP_SIG" = 1 ]; then warn "--skip-signature: the SHA256SUMS signature is NOT verified"
  elif [ -f "$dir/SHA256SUMS.minisig" ]; then sig_ok "$dir/SHA256SUMS" "$dir/SHA256SUMS.minisig" || fail DEY-I006
  else warn "no SHA256SUMS.minisig next to $LOCAL: the signature is NOT verified"; fi
  sum_ok "$ARCHIVE" "$dir/SHA256SUMS" || fail DEY-I005 "${ARCHIVE##*/}"
}

# Paths of spec section 2. /etc/deyroute and /var/lib/deyroute belong to group deyroute so backend
# processes (user deyroute) can reach backends/ and bin/ (QUESTIONS.md C.16).
setup_user_dirs() {
  local -a g=(--user-group)
  if ! getent passwd deyroute >/dev/null 2>&1; then
    if getent group deyroute >/dev/null 2>&1; then g=(--gid deyroute); fi
    if have useradd; then useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin "${g[@]}" deyroute
    else systemd-sysusers --inline 'u deyroute - "DEYROUTE tunnel backends" /nonexistent /usr/sbin/nologin'; fi
  fi
  install -d -m 0710 -o root -g deyroute /etc/deyroute
  install -d -m 0700 -o root -g root /etc/deyroute/secrets
  install -d -m 0750 -o root -g deyroute /etc/deyroute/backends /var/lib/deyroute /var/log/deyroute
  install -d -m 0755 -o root -g root /var/lib/deyroute/bin
  install -d -m 0700 -o root -g root /var/lib/deyroute/backups
  install -d -m 0770 -o root -g deyroute /var/log/deyroute/tunnels
}
install_binary() {
  local bin; mkdir -p "$TMP/x"
  tar -xzf "$ARCHIVE" -C "$TMP/x" 2>/dev/null || fail DEY-I005 "${ARCHIVE##*/}"
  bin=$(find "$TMP/x" -type f -name deyroute -print -quit)
  [ -n "$bin" ] || fail DEY-I005 "${ARCHIVE##*/}"
  install -m 0755 "$bin" "$BIN_DIR/.deyroute.new" # atomic: temp file, then rename; test-run it here (/tmp may be noexec)
  "$BIN_DIR/.deyroute.new" version >/dev/null 2>&1 || { rm -f "$BIN_DIR/.deyroute.new"; die DEY-I003 "Unsupported CPU architecture: $(uname -m)" \
    "the deyroute binary from ${ARCHIVE##*/} does not run on this server" "use the deyroute_<version>_linux_${ARCH}.tar.gz archive"; }
  if [ -f "$BIN_DIR/deyroute" ]; then install -m 0755 "$BIN_DIR/deyroute" /var/lib/deyroute/bin/deyroute.prev; fi
  mv -f "$BIN_DIR/.deyroute.new" "$BIN_DIR/deyroute"
  ln -sfn deyroute "$BIN_DIR/dey"
  say "installed $("$BIN_DIR/deyroute" version 2>/dev/null | awk 'NR == 1')"
}
launch() { rm -rf "$TMP"; exec "$BIN_DIR/deyroute" "$@"; }
run_setup() {
  local u; local -a args=()
  if [ -f /etc/deyroute/config.yaml ]; then
    say "existing installation found: repairing/upgrading (config untouched)"
    if [ -n "$JOIN$ROLE$NAME" ]; then warn "setup/join options ignored: this server is already set up"; fi
    for u in deyroute-hub deyroute-node; do
      if systemctl is-active --quiet "$u.service"; then systemctl restart "$u.service"; say "restarted $u"; fi
    done
    return 0
  fi
  if [ "$NO_SETUP" = 1 ]; then say "next: deyroute setup   (or: deyroute restore FILE)"; return 0; fi
  if [ -n "$NAME" ]; then args+=(--name "$NAME"); fi
  if [ -n "$JOIN" ]; then launch join "$JOIN" "${args[@]}"; fi
  if [ -n "$ROLE" ]; then args=(--role "$ROLE" "${args[@]}"); fi
  if [ "$YES" = 1 ]; then args+=(--yes); fi
  if [ ${#args[@]} -gt 0 ]; then launch setup "${args[@]}"; fi
  if { : </dev/tty; } 2>/dev/null; then launch setup </dev/tty; fi
  die DEY-I014 "Setup step failed: wizard" "no terminal is available for the interactive setup" \
    "run: deyroute setup   (or re-run with --role hub --name NAME --yes)"
}

main() {
  parse_args "$@"
  check_system
  TMP=$(mktemp -d)
  trap 'rm -rf "$TMP"' EXIT
  if [ -n "$LOCAL" ]; then use_local; else fetch_release; fi
  setup_user_dirs
  install_binary
  run_setup
}
trap 'echo "install.sh: unexpected failure at line $LINENO" >&2' ERR
main "$@"
