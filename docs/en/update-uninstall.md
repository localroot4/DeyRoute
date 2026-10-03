# Update & uninstall

[فارسی](../fa/update-uninstall.md) · [Index](index.md)

Updates never run without asking you. Updating deyroute does not interrupt the
tunnels: the tunnel programs run as their own systemd units and are not
touched while the deyroute service restarts.

## Update deyroute

Run on the hub:

```bash
deyroute update --check            # is there a new release? shows the changelog
deyroute update                    # changelog, confirmation, download, verify, replace, restart
deyroute update --version 1.2.0 --yes
deyroute update --rollback         # back to the previous binary
```

Menu: `11) Update` → `1) Check for updates`, `2) Apply update`,
`5) Roll back`.

What `deyroute update` does:

1. checks for a release and shows what changed: the sections of the
   release's `CHANGELOG.md` after your version (at most 40 lines, then the
   release page), fetched like the release itself (through a node or your
   mirror) and checked against the signed `SHA256SUMS`; a release without
   it shows the release page address instead;
2. asks for confirmation (`--yes` skips it; without a terminal `--yes` is
   required);
3. downloads the release and verifies its checksum and signature — through
   a node when the hub cannot reach GitHub (`DEY-N012` if no node is online),
   or from your mirror;
4. replaces `/usr/local/bin/deyroute`, keeping the old one as
   `/var/lib/deyroute/bin/deyroute.prev`, and restarts the deyroute service;
5. the nodes follow the hub's version.

Use only one of `--check`, `--version` and `--rollback` at a time.
`--rollback` restores `deyroute.prev` (`DEY-S007` when there is none; then
install a specific version with `--version V`). It also works when the
service does not run any more (for example a bad update that keeps
crashing) and on a node: the binaries are swapped locally and the service
is restarted.

**Alternative:** running the installer again also upgrades in place and keeps
the configuration. It works even when the service is down, and on a node:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh)
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) --version 1.2.0
```

Hub and nodes must stay on the same major.minor version (see
[Join](join.md#incompatible-versions)).

## Update the backends

The tunnel programs (Backhaul, Rathole, FRP, Xray, Hysteria2, Waterwall …)
are pinned to tested versions in a signed manifest. To update them:

```bash
deyroute update backends                 # every backend
deyroute update backends backhaul --yes  # one backend
```

Menu: `11) Update` → `3) Update backends`.

The new version is installed next to the old one on the hub and on every
node, the configurations are rendered again, and an active transport whose
backend changed restarts. If its probe is not green within 60 seconds, it
goes back to the previous version automatically (event
`backend_update_rolled_back`, `DEY-S003`). When failover already moved the
tunnel to another rung or node meanwhile, the rollback brings it back to the
rung it used before the update. The result is a table:

```text
BACKEND   FROM     TO       RESULT      DETAIL
backhaul  <old>    <new>    updated
rathole   <same>   <same>   unchanged
```

(`RESULT` is `updated`, `rolled_back`, `unchanged` or `failed`.)

The pinned versions and their upstream documentation are listed on each
backend page: [backhaul](../backends/backhaul.md),
[rathole](../backends/rathole.md), [frp](../backends/frp.md),
[xray](../backends/xray.md), [hysteria2](../backends/hysteria2.md),
[waterwall](../backends/waterwall.md), [wireguard](../backends/wireguard.md),
[direct](../backends/direct.md), [gost](../backends/gost.md),
[chisel](../backends/chisel.md).

## Update the backend manifest

```bash
deyroute update manifest
```

Menu: `11) Update` → `4) Backend manifest`. Fetches the newest signed
manifest (versions, download URLs and sha256 of every backend) and prints the
versions. A backend binary is never installed without a matching sha256:
`DEY-S001` means a file did not match (it was rejected, nothing changed),
`DEY-S006` means the manifest has no checksum for a backend. The new manifest
is saved as `/etc/deyroute/backends.yaml` (it overrides the one built into
deyroute). Running tunnels keep their backend versions until you run
`deyroute update backends`.

## Uninstall

```bash
deyroute uninstall
```

Menu: `12) Settings` → `3) Uninstall`. It asks:

```text
Keep the backups in /var/lib/deyroute/backups?
   Type y (yes) or n (no) and press Enter. Enter alone = y (yes).
Also uninstall deyroute from every online node?     (hub only)
   Type y (yes) or n (no) and press Enter. Enter alone = n (no).
```

and then lists exactly what goes away, and you type `yes`:

```text
Uninstall removes from this server:
  - every deyroute tunnel unit and the deyroute service (stopped and disabled)
  - the nftables table inet deyroute
  - the kernel settings of deyroute (restored from sysctl-before-deyroute.conf)
  - /etc/deyroute: configuration, secrets and certificates
  - /var/lib/deyroute: state and backend binaries, except the backups in /var/lib/deyroute/backups
  - /var/log/deyroute, the deyroute system user and group, and the deyroute binary
Every tunnel stops.
```

| Flag | Meaning |
| --- | --- |
| `--keep-backups` | keep `/var/lib/deyroute/backups` |
| `--nodes` | on a hub: uninstall deyroute from every online node first |
| `--yes` | no questions (for scripts) |

With `--yes` nothing is asked, so backups are **deleted** unless you also
pass `--keep-backups`:

```bash
deyroute uninstall --keep-backups --yes
```

Only DEYROUTE's own things are removed: other nftables tables and firewall
rules, your VPN service, other packages and your files stay as they are.
If a step fails, the others still run, the binary is kept and you see
`DEY-I022`; fix the cause and run `deyroute uninstall` again (it is safe to
repeat).

Removing a node **from the hub** (`deyroute node remove <id>`) does not clean
the node server; run `deyroute uninstall` on the node for that.
