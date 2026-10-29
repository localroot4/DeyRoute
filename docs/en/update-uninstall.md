# Update & uninstall

[فارسی](../fa/update-uninstall.md) · [Index](index.md)

You update deyroute on the hub when you choose, and the nodes follow it.
The hub can also update itself once a day (automatic update, off by
default; see below). Updating deyroute does not interrupt the tunnels: the
tunnel programs run as their own systemd units and are not touched while
the deyroute service restarts.

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
5. the nodes follow the hub's version (see
   [How the nodes follow](#how-the-nodes-follow)).

Use only one of `--check`, `--version` and `--rollback` at a time.
`--rollback` restores `deyroute.prev` (`DEY-S007` when there is none; then
install a specific version with `--version V`). It also works when the
service does not run any more (for example a bad update that keeps
crashing) and on a node: the binaries are swapped locally and the service
is restarted.

While `config.yaml` uses a setting that an older release does not know,
`--rollback` and `--version` to such a release are refused with `DEY-S011`:
that release would refuse the file and the service would not start. The
error names each setting with the first release that knows it; remove the
setting first, then repeat the command. For `--rollback` the hub compares
the version it recorded for `deyroute.prev` when an update or rollback
wrote that file. When the installer or a rollback without the service has
replaced the file since, its version is unknown, and the rollback is
refused while any such setting is in use.

## How the nodes follow

When a node connects with another version than the hub's after an update
or a rollback, the hub tells it to install the hub's version. The hub does
the same without an update:

- for a node of another major.minor (incompatible, `DEY-N004`), as soon as
  it connects: a hub restored on a newer release, or a node installed from
  an older one. Then every other node follows too;
- after a restore from a backup written by another version
  ([Hub move](hub-move.md)): every node follows;
- after `deyroute update` when the hub already runs the newest release:
  nothing is installed on the hub, and every node of another version (an
  older build of the same major.minor too) follows. The command says how
  many (`nodes_queued` in `--json`).

A node newer than the hub goes back to the hub's version (a warning in the
hub's log): hub and node must share major.minor, and the hub's version
wins. A hub on a development build pushes nothing by itself. A node of the
same major.minor that only runs an older build is left alone until the
next `deyroute update`.

A node installs a new deyroute only when the release it comes from is signed
with the DEYROUTE release key built into the node; what the hub says
about it is not enough:

- The hub keeps the signed files of its release in
  `/var/lib/deyroute/bin/<version>/`: `SHA256SUMS`, `SHA256SUMS.minisig`,
  the archive `deyroute_<version>_linux_<arch>.tar.gz` of each
  architecture it serves, and `backends.yaml` with its signature.
  `deyroute update` and the installer (also with `--local`) leave them
  there. A hub without them (installed before this change, or with
  `--skip-signature`) downloads them the first time a node needs them,
  like the release itself: through a node or your mirror. When the hub
  starts it removes the files of versions it neither runs nor rolls back
  to.
- The node downloads these files from the hub and checks them itself:
  the signature of `SHA256SUMS`, that `SHA256SUMS` lists the archive of
  exactly the hub's version, the checksum of the archive, and that the
  archive holds one regular file `deyroute` (no link, no path outside the
  archive, at most 200 MB) that runs on this server.
- Last, the node asks that binary itself: `deyroute --json version` must
  answer `"signed_update": true`. Releases built before signed updates do
  not say it. They are signed with the same key, but a node on one of them
  would take any binary its hub serves, so a node never moves to one: the
  answer is `DEY-S001` with "… was built before signed updates", and the
  node keeps its version. A hub that serves signed releases asks only for
  its own version, so this happens only when something on the hub asked
  for an older release.
- Only then the node replaces its binary (the old one stays as
  `deyroute.prev`) and restarts; the tunnels keep running.
- When a check fails the node installs nothing and keeps running its
  version. The hub's log has the line "node could not install the hub's
  version" with `DEY-S001` and the check that failed (for example
  "SHA256SUMS.minisig is not a valid signature …" or "… does not match the
  signed SHA256SUMS"); the hub asks again when the node reconnects, at
  the earliest 10 minutes after the failure, then 20, 40 … up to 6 hours
  (a restart of the hub asks at once). Only the first failure is a
  warning in the log. The
  hub serves its files as they are: when a node keeps refusing them with
  "… does not match the signed SHA256SUMS" (a damaged file on the hub),
  delete `/var/lib/deyroute/bin/<version>/` on the hub, and it downloads
  the release again the next time a node needs it.

**Update the hub first.** A node of this version updates only from a hub
that serves signed releases. A hub of an earlier build cannot: the node
refuses its update with `DEY-S001` ("update the hub first") and keeps
running. Update the hub, then the nodes follow. A node of an earlier build
still updates from a new hub as before. The same holds after
`deyroute update --version` or `--rollback` on the hub to a build before
signed updates: the nodes keep their version until the hub runs a newer
build again.

**Development builds.** A hub on a development build (`deyroute version`
shows `dev`) has no signed release, so its nodes refuse with `DEY-S012`:
install a release on the hub. Only in a test lab, the line
`allow_unsigned_updates: true` under `node:` in a node's
`/etc/deyroute/config.yaml` lets that node take the hub's binary without
a signature (from an earlier hub too). Never set it on a server with real
users: with it, whoever controls the hub controls the node again. While
it is set, the node refuses to install a release older than **0.3.0**
(`DEY-S011`, every `0.3.0-edge.N` older than the node included): such a
release does not know the key and would not start.

## Automatic update

Off by default: deyroute never installs a release unless you ask. When you
turn it on, once a day at **04:00 server time** the hub installs the newest
release that has been out for at least **24 hours**; the nodes follow it as
after `deyroute update`, and the tunnels keep running.

After the restart the hub checks that every tunnel that was UP and every
node that was online before the update works again (it gives them up to 10
minutes). If not — or if the new version does not even start — it puts the
previous version back by itself, records a `DEY-S003` event and a dashboard
line, and never installs that release automatically again (you still can,
with `deyroute update`). The previous version ran with your `config.yaml`
before the update, so it loads it again. Only when you set something after
the update that the previous version does not know would it refuse the
file: then the new version keeps running, the event `update_rolled_back`
is recorded with the code `DEY-S011` (Telegram sends it with the `update`
events), and the dashboard shows `DEY-S011` for 7 days (details in
`deyroute update auto`).

```bash
deyroute update auto        # on or off, next release and when, last result
deyroute update auto on     # turn it on
deyroute update auto off    # turn it off again
```

```text
── AUTOMATIC UPDATE ─────────────────────────────────────────
  Status     on
  When       every day at 04:00 server time; only releases out for 24 hours
  Running    0.3.0-edge.40
  Next       0.3.0-edge.41 on 2026-10-10 04:00
  Last       updated 0.3.0-edge.39 → 0.3.0-edge.40; 1 tunnels and 1 nodes work (2026-10-09 04:03)
```

The switch is `hub.auto_update` in `config.yaml`: `update auto on` writes
`auto_update: true` (after an automatic backup), and `off` removes the
key, so a backup keeps your choice and a restored hub has it again. Without
the key the automatic update is off. To choose at setup, pass
`--auto-update on` or `--auto-update off` (the default):

```bash
deyroute setup --role hub --name ir-1 --yes --auto-update on
```

`deyroute --json update auto` reports `"enabled"` and `"source": "config"`.

While it is on, releases before **0.3.0** do not know the key, so
`deyroute update --rollback` and `deyroute update --version V` to such a
release (every `0.3.0-edge.N` build included) are refused with `DEY-S011`;
run `deyroute update auto off` first. Updates to newer releases are not
affected, and a failed automatic update still goes back to the version it
came from: that version ran with the key.

**Hubs that ran an earlier build:** before this change the automatic update
was on by default, and the switch was kept in the state database, which a
backup does not contain. After the update it is **off** until you turn it
on with `deyroute update auto on`, also on a hub where it ran before. Such a
hub records the event `auto_update_default_off` once, and `deyroute status`
shows "automatic update is now off by default; turn it on with: deyroute
update auto on" for 7 days. A new installation shows neither. deyroute
also keeps writing the switch where earlier builds read it (in the state
database, with every change of the automatic update's record and with
`update auto on|off`), so after `update --rollback` or `update --version`
to an earlier build the automatic update stays as it was here: off unless
you turned it on.

While it is on, `deyroute status` does not list a new release as something
that needs attention: it is installed on schedule. A manual
`deyroute update --rollback` also tells the automatic update to skip the
release you left.

**Alternative:** running the installer again also upgrades in place and keeps
the configuration. It works even when the service is down, and on a node:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh)
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/download/v1.2.0/install.sh) --version 1.2.0
```

Hub and nodes must stay on the same major.minor version (see
[Join](join.md#incompatible-versions)).

## Which versions stay available

- Stable releases (`vX.Y.Z`) are never removed.
- Development builds (`…-edge.N`) are removed once 10 newer builds of the
  same branch exist (`main` and `dev` are counted separately); the release
  marked *latest* is never removed. A hub on a development build can join
  nodes and reinstall its own version until then.
- A release that is no longer published cannot be installed: its own
  installer is gone too (`curl` reports `404`), and an installer run with
  `--version` of it stops with `DEY-I024`. Pick a version from the
  [releases page](https://github.com/localroot4/DeyRoute/releases), or
  install from a copy you saved (`--local`, see
  [Install](install.md#when-github-is-not-reachable-from-the-hub)).

To keep servers on one version for a long time, use a stable release, or
keep its files (`install.sh`, `SHA256SUMS`, `SHA256SUMS.minisig` and the
archives) yourself.

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
deyroute), with its signature next to it as
`/etc/deyroute/backends.yaml.minisig`; your backups keep both. Running
tunnels keep their backend versions until you run
`deyroute update backends`.

### Backends on the nodes

A node installs a backend only in a version a signed manifest pins: the
version, download address and sha256 (for the node's architecture) the hub
asks for must be exactly those in the manifest built into the node, or in a
manifest the hub sends along whose signature the node checks itself with
the release key built into it. Anything else is refused before a download:
the hub's log has the line "backend installation on a node failed" with
`DEY-N050` and the reason ("… is not pinned by this node's built-in
backends.yaml …"), and the transports of that backend are not used on that
node.

- **Your manifest override.** The hub sends it with its signature to the
  nodes. A hub that saved the override with an earlier build has no
  signature for it: when it starts it downloads one that matches the file
  (from the newest release, then from its own), and tries again every 10
  minutes. Until it has one, its nodes install only the versions of their
  own built-in manifest, and the hub's log warns "the backend manifest
  override has no valid signature …". Run `deyroute update manifest` on
  the hub to fix it at once. Do not edit `/etc/deyroute/backends.yaml` by
  hand: the signature would no longer match, and the nodes would get none
  of its versions.
- **Hub and node on different builds.** Each build has its own built-in
  manifest. When a node's differs from the hub's, the hub sends the signed
  manifest of its own release, which it keeps in
  `/var/lib/deyroute/bin/<version>/` and downloads the first time a node
  needs it. A hub on a development build has none: its nodes install only
  the versions of their own built-in manifest.
- **A hub of an earlier build** sends no manifest; the nodes of this
  version then install only the versions of their own built-in manifest.
  Update the hub first (see [How the nodes follow](#how-the-nodes-follow)).
- **Older signed manifests.** A node checks that a manifest is signed,
  not how old its release is: a hub can still send the signed manifest of
  an older release and so pin an older backend version that release
  shipped (an open question in QUESTIONS.md).
- **Versions already installed.** After `deyroute update`, running tunnels
  keep their backend versions until you run `deyroute update backends`, and
  the new build's manifest may pin newer ones. A node that already has such
  a version installed (its files match the sha256 the hub names) keeps
  using it without a manifest: nothing is downloaded. A node that does not
  have it yet (a new node) cannot install it, so those transports are not
  used there: run `deyroute update backends` after an update that brings
  new backend versions.

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
