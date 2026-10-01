// Package install implements everything that brings files onto a server
// (spec sections 5, 7 and 11):
//
//   - the download chain (owner mirror → release base → GitHub releases, and
//     "via node" for the hub, plugged in as a Fetcher), each source tried 3
//     times with exponential backoff;
//   - supply-chain verification: SHA256SUMS parsing, sha256 checks and minisign
//     signatures against the release key hard-coded in MinisignPublicKey;
//   - archive extraction (tar.gz, zip, gz, raw) without path traversal;
//   - the backend binary layout /var/lib/deyroute/bin/<backend>/<version>/ with a
//     <bin>.sha256 file next to each binary (never overwritten);
//   - self-update of /usr/local/bin/deyroute with deyroute.prev rollback;
//   - backup/restore of /etc/deyroute (tar.gz, age passphrase encryption) and the
//     automatic pre-apply backups (backups/auto/, last 20 kept);
//   - the uninstall file plan.
//
// Every filesystem path is resolved under an injectable root directory so the
// whole package is testable in t.TempDir() without root privileges. The
// package never executes external programs.
package install
