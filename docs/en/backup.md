# Backup & restore

[فارسی](../fa/backup.md) · [Index](index.md)

A backup is one file with everything needed to rebuild a hub (or a node):
configuration, keys and certificates, and the event history. With it you can
undo a mistake, rebuild a broken server, or [move the hub](hub-move.md)
without re-joining the nodes.

## Create a backup

```bash
deyroute backup
```

```text
Backup passphrase:
Repeat the passphrase:
Backup written: /var/lib/deyroute/backups/deyroute-backup-20260930T120000Z.tar.gz.age
Keep the passphrase safe: the backup cannot be restored without it.
```

Menu: `10) Backup & Restore` → `1) Create backup`.

| Option | Meaning |
| --- | --- |
| `--out FILE` | write to `FILE` instead of `/var/lib/deyroute/backups/deyroute-backup-<UTC>.tar.gz.age` |
| `--no-encrypt` | write a plain `tar.gz` — it contains the CA key and every secret, keep it private |

The file is encrypted with [age](https://age-encryption.org) using your
passphrase and saved with mode 0600. **There is no way to open it without the
passphrase.**

Copy the file off the server (for example `scp` to your computer). A backup
that lives only on the server is lost with the server, and
`deyroute uninstall` deletes `/var/lib/deyroute/backups` unless you keep it.

### In scripts

Without a terminal the passphrase comes from `DEYROUTE_BACKUP_PASSPHRASE`:

```bash
DEYROUTE_BACKUP_PASSPHRASE='long secret phrase' deyroute backup --out /root/hub.tar.gz.age --json
```

Without the variable and without `--no-encrypt` the command stops with
`DEY-S008` (a backup passphrase is required). Keep the variable out of shell
history, for example by reading it from a root-only file.

## What is in a backup

| Included | Not included |
| --- | --- |
| all of `/etc/deyroute`: `config.yaml`, `secrets/` (internal CA and its key, hub or node keys and certificates, tunnel tokens and TLS certificates, Telegram token file in its default place), the rendered backend configs, a `backends.yaml` override | `state.db`: live tunnel state, probe history, metrics |
| the event history of the hub (when the hub service is running) | backend binaries (`/var/lib/deyroute/bin`; downloaded again) |
| `manifest.json`: deyroute version, creation time, role, name | logs (`/var/log/deyroute`) |
| | your VPN service on the node (Xray, Marzban …) and its users |

When the hub service is not running, the backup is still written but without
events (`The daemon is not running: the backup has no event history.`). A
node keeps no event history; its backup holds its configuration and
certificates.

## Automatic backups

Before every change it applies — adding a tunnel, changing ports, `deyroute
config apply` or `config edit`, … — the hub saves a backup of `/etc/deyroute`
in `/var/lib/deyroute/backups/auto/`. The last 20 are kept. They are **not
encrypted** (the directory is readable by root only) and have no event
history.

Use them to undo a bad change:

```bash
ls /var/lib/deyroute/backups/auto/
deyroute restore /var/lib/deyroute/backups/auto/deyroute-backup-20260930T115500.123456789Z.tar.gz
```

## Restore

```bash
deyroute restore /root/deyroute-backup-20260930T120000Z.tar.gz.age
```

Menu: `10) Backup & Restore` → `2) Restore from a backup`.

1. The file is decrypted (passphrase asked once, or taken from
   `DEYROUTE_BACKUP_PASSPHRASE`) and checked. Nothing changes if it is not a
   valid backup.
2. Its `config.yaml` is validated; an older schema is migrated.
3. If this is a hub backup and this server has another public IP, you are
   asked whether the hub moved here ([Hub move](hub-move.md)).
4. You confirm by typing `yes`:

   ```text
   Restoring deyroute-backup-….tar.gz.age (hub ir-1, created 2026-09-30 12:00 with deyroute 1.0.0) replaces this server's /etc/deyroute: configuration, secrets and certificates. The current directory is kept as /etc/deyroute.pre-restore-<time>; every change made after that backup is lost. Tunnels are re-rendered and restarted.
   ```

5. Steps: `Restore backup`, `Save events for import`, `Apply kernel
   settings`, `Install and start service`. The service re-creates every
   tunnel from the restored configuration.

The previous `/etc/deyroute` is kept as `/etc/deyroute.pre-restore-<time>`; delete
it when you no longer need it. In scripts add `--yes` (and set
`DEYROUTE_BACKUP_PASSPHRASE`):

```bash
DEYROUTE_BACKUP_PASSPHRASE='long secret phrase' deyroute restore /root/hub.tar.gz.age --yes
```

A node backup is restored the same way on a node; the node then reconnects
to its hub by itself. Restoring works on a server that was set up with
`install.sh --no-setup`.

## Errors

| Code | Meaning | What to do |
| --- | --- | --- |
| `DEY-S004` | cannot decrypt the backup | wrong passphrase, or a damaged file |
| `DEY-S005` | not a valid deyroute backup | use a file made by `deyroute backup` |
| `DEY-S008` | passphrase required | type it on a terminal, set `DEYROUTE_BACKUP_PASSPHRASE`, or `--no-encrypt` |
| `DEY-C006` / `DEY-C019` | the configuration cannot be migrated / unknown schema | update deyroute (`deyroute update`) or use another backup |

## Good habits

- Take a backup after adding nodes or tunnels, and before updating.
- Keep at least two copies outside the server, and the passphrase somewhere
  else than the file.
- Test a restore once on a spare server, so you know it works when you need
  it.
