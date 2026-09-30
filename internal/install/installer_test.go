package install

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Static checks of installer/install.sh (spec section 5). The package may not
// run programs (only internal/exec may import os/exec), so `bash -n` and
// shellcheck run in CI (see .github/workflows) and not here.

func readInstaller(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "installer", "install.sh"))
	require.NoError(t, err)
	return string(data)
}

func TestInstallerSyntax(t *testing.T) {
	t.Skip("bash -n installer/install.sh and shellcheck run in CI; tests may not exec programs")
}

func TestInstallerStatic(t *testing.T) {
	s := readInstaller(t)
	lines := strings.Count(s, "\n")
	require.LessOrEqual(t, lines, 250, "install.sh must stay within 250 lines (spec section 5)")
	require.True(t, strings.HasPrefix(s, "#!/usr/bin/env bash\n"))
	require.Contains(t, s, "\nset -Eeuo pipefail\n")

	// Flags and the non-interactive forms of section 5.
	for _, flag := range []string{"--version", "--mirror", "--local", "--no-setup", "--skip-signature",
		"--role", "--name", "--yes", "--help", "join)", "DEYROUTE_MIRROR"} {
		require.Contains(t, s, flag)
	}
	require.Contains(t, s, `launch setup "${args[@]}"`)
	require.Contains(t, s, `launch join "$JOIN" "${args[@]}"`)
	require.Contains(t, s, "existing installation found: repairing/upgrading (config untouched)")

	// Release files, verification and atomic install.
	for _, want := range []string{"SHA256SUMS", "SHA256SUMS.minisig", "deyroute_", "_linux_", "sha256sum",
		"minisign -V", "openssl pkeyutl -verify", "-blake2b512", "for try in 1 2 3", "$((try * 2))",
		`install -m 0755 "$bin" "$BIN_DIR/.deyroute.new"`, `mv -f "$BIN_DIR/.deyroute.new" "$BIN_DIR/deyroute"`,
		`ln -sfn deyroute "$BIN_DIR/dey"`, "useradd --system", "systemd-sysusers", "trap 'rm -rf \"$TMP\"' EXIT",
		"/var/lib/deyroute/bin/deyroute.prev"} {
		require.Contains(t, s, want)
	}

	// Directory modes of section 2.
	for _, want := range []string{
		"install -d -m 0710 -o root -g deyroute /etc/deyroute\n",
		"install -d -m 0700 -o root -g root /etc/deyroute/secrets\n",
		"install -d -m 0750 -o root -g deyroute /etc/deyroute/backends /var/lib/deyroute /var/log/deyroute\n",
		"install -d -m 0755 -o root -g root /var/lib/deyroute/bin\n",
		"install -d -m 0700 -o root -g root /var/lib/deyroute/backups\n",
		"install -d -m 0770 -o root -g deyroute /var/log/deyroute/tunnels\n",
	} {
		require.Contains(t, s, want)
	}

	// The package-manager hints cover apt, dnf and pacman.
	for _, pm := range []string{"apt-get install -y", "dnf install -y", "pacman -S --noconfirm"} {
		require.Contains(t, s, pm)
	}
}

func TestInstallerConstantsMatchGo(t *testing.T) {
	s := readInstaller(t)
	get := func(name string) string {
		m := regexp.MustCompile(`(?m)^` + name + `="([^"]*)"$`).FindStringSubmatch(s)
		require.NotNil(t, m, "%s not found at the top of install.sh", name)
		return m[1]
	}
	require.Equal(t, MinisignPublicKey, get("MINISIGN_PUBKEY"), "installer and binary must pin the same release key")
	require.Equal(t, ReleaseBase, get("RELEASE_BASE"))
	require.Equal(t, GitHubReleases, get("GITHUB_BASE"))
}

func TestInstallerErrorCodes(t *testing.T) {
	s := readInstaller(t)
	for i := 1; i <= 12; i++ {
		code := deyerr.Code(fmt.Sprintf("DEY-I%03d", i))
		require.Contains(t, s, "die "+string(code)+" ", "install.sh must stop with %s", code)
	}
	// Every code in the script exists in the catalog, and the fixed
	// messages are the catalog's exact text.
	for _, c := range regexp.MustCompile(`DEY-[A-Z][0-9]{3}`).FindAllString(s, -1) {
		info, ok := deyerr.Lookup(deyerr.Code(c))
		require.True(t, ok, "%s is not in internal/errors/codes.go", c)
		if !strings.Contains(info.Message, "{") {
			require.Contains(t, s, `"`+info.Message+`"`, "%s message differs from the catalog", c)
		}
	}
	for _, code := range []deyerr.Code{deyerr.I001, deyerr.I002, deyerr.I003, deyerr.I004, deyerr.I005,
		deyerr.I006, deyerr.I008, deyerr.I009, deyerr.I012} {
		info, _ := deyerr.Lookup(code)
		require.Contains(t, s, `"`+info.Why+`"`, "%s Why differs from the catalog", code)
	}
	// The three-line format of section 13.
	require.Contains(t, s, `printf '✖ %s  %s\n  Why:  %s\n  Fix:  %s\n'`)
}
