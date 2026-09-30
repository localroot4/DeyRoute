# Install

[فارسی](../fa/install.md) · [Index](index.md)

Installing is always one line. Running the same line again repairs or
upgrades the installation; it never starts over and never touches your
configuration.

## Requirements

| | |
| --- | --- |
| Tier 1 (tested) | Ubuntu 22.04, 24.04, 26.04 · Debian 12, 13 |
| Tier 2 (should work) | Ubuntu 20.04 · Debian 11 · Rocky / AlmaLinux 8, 9, 10 · Fedora (last two) · Arch Linux |
| CPU | amd64 (x86_64) or arm64 (aarch64) |
| System | root, systemd 245 or newer, Linux kernel 5.4 or newer |
| Packages | `iproute2`, `nftables` or `iptables`, `ca-certificates`, and `curl` or `wget` for the installer |

Nothing else is needed: no Python, no Docker, no Node.js. The hub and the
nodes use the same program.

The installer checks every requirement and stops with a code and a one-line
fix when something is missing:

| Code | Problem | Fix |
| --- | --- | --- |
| `DEY-I001` | not running as root | `sudo -i`, then run the line again |
| `DEY-I002` / `DEY-I008` | no systemd / systemd older than 245 | use a supported distribution |
| `DEY-I003` | CPU is not amd64 or arm64 | use another server |
| `DEY-I009` | kernel older than 5.4 | upgrade the kernel or the distribution |
| `DEY-I007` | neither curl nor wget | `apt-get install -y curl` |
| `DEY-I010` | `ip` command missing | `apt-get install -y iproute2` (dnf: `iproute`) |
| `DEY-I011` | neither nftables nor iptables | `apt-get install -y nftables` |
| `DEY-I012` | CA certificates missing | `apt-get install -y ca-certificates` |

## Install the hub (Iran server)

As root:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh)
```

The installer downloads and verifies the program, then starts the setup
wizard. The wizard asks at most five questions; Enter accepts the value in
brackets:

```text
DEYROUTE setup: a few questions; Enter accepts the value in brackets.
Role of this server: 1) hub (Iran, users connect here)  2) node (abroad, runs your VPN service) [hub]: 1
Name of this hub [myhost]: ir-1
Public IP of this server [5.6.7.8]:
Control port for the nodes [44433]:
Apply the balanced kernel profile (BBR, larger buffers; undo with: deyroute optimize revert)? [Y/n]
```

- **Public IP**: detected automatically. If the detected address is private or
  CGNAT, the wizard says so; type the address users connect to (see your
  provider panel).
- **Control port**: the port the nodes connect to. `44433` by default; when it
  is taken, the next free port is offered.
- **Kernel profile**: writes `/etc/sysctl.d/99-deyroute.conf` (BBR, larger
  buffers). The previous values are saved and `deyroute optimize revert`
  restores them.

Everything else is automatic. The wizard shows each step:

```text
  ✔ Detect public IP
  ✔ Create internal CA
  ✔ Issue hub certificate
  ✔ Apply firewall (table inet deyroute)
  ✔ Apply kernel settings
  ✔ Write /etc/deyroute/config.yaml
  ✔ Install and start service

Hub ir-1 is ready: 5.6.7.8, control port 44433.
Run this command on each node (valid until 12:15, 15m):

bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) join 'dey://…@5.6.7.8:44433#sha256:…' --version 1.0.0
```

That join command works **once**, for one node. Create a new one for every
further node: `deyroute node join-command`. Continue with [Join a node](join.md).

After setup, `deyroute` (or `dey`) opens the menu and `deyroute status` prints the
dashboard.

## Non-interactive install

Pass the answers as flags; `--yes` accepts every default:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) --role hub --name ir-1 --yes
```

With `--yes` the public IP is detected, the control port is 44433 (or the next
free one) and the balanced kernel profile is applied. The same flags work on
an installed binary:

```bash
deyroute setup --role hub --name ir-1 --yes
deyroute setup --role hub --name ir-1 --control-port 44500
```

Without a terminal and without flags, setup stops with `DEY-I014` (no
terminal for the wizard). A node is never set up this way: it always joins
with the link from the hub (`install.sh join 'dey://…'`).

## Installer options

| Option | What it does |
| --- | --- |
| `join 'dey://…' [--name N]` | install and join a hub as a node (see [Join](join.md)) |
| `--role hub\|node --name N --yes` | non-interactive setup |
| `--version V` | install release `V` instead of the latest |
| `--mirror URL` | try this mirror first (same as the environment variable `DEYROUTE_MIRROR`) |
| `--local FILE` | offline install from `deyroute_<ver>_linux_<arch>.tar.gz` |
| `--no-setup` | install only; then run `deyroute setup` or `deyroute restore FILE` |
| `--skip-signature` | do not verify the signature of `SHA256SUMS` (testing only) |
| `-h`, `--help` | show the options |

## When GitHub is not reachable from the hub

GitHub is often blocked or slow from Iranian datacenters. Only the **first**
install of the hub needs a download source: once a node has joined, the hub
downloads backend binaries and updates **through the node**, so the hub
itself does not need GitHub any more.

**Option 1 — your own mirror.** Copy the release files to any HTTPS server
you control, in this layout:

```text
<base>/latest/install.sh
<base>/latest/SHA256SUMS
<base>/latest/SHA256SUMS.minisig
<base>/latest/deyroute_<version>_linux_amd64.tar.gz
<base>/latest/deyroute_<version>_linux_arm64.tar.gz
<base>/v<version>/…              (the same files, for --version)
```

Then install with the mirror:

```bash
DEYROUTE_MIRROR=https://dl.example.com/deyroute bash <(curl -fsSL https://dl.example.com/deyroute/latest/install.sh)
```

The files are still verified against the release signature, so a mirror
cannot change them. When `DEYROUTE_MIRROR` is set in the environment during
setup, it is saved as `hub.mirror` and the join commands the hub prints use
`<mirror>/latest/install.sh` too. (`--mirror URL` affects only the installer
run itself.)

**Option 2 — offline file.** On a computer with internet access, download
from the [releases page](https://github.com/localroot4/DeyRoute/releases)
`install.sh`, `SHA256SUMS`, `SHA256SUMS.minisig` and the archive for your
CPU. Copy all four to the hub into one directory and run:

```bash
scp install.sh SHA256SUMS SHA256SUMS.minisig deyroute_1.0.0_linux_amd64.tar.gz root@HUB_IP:/root/
bash /root/install.sh --local /root/deyroute_1.0.0_linux_amd64.tar.gz
```

Keep `SHA256SUMS` and `SHA256SUMS.minisig` next to the archive: without them
the installer warns that nothing is verified.

The installer honours `https_proxy` if you need a proxy.

## What the installer verifies

1. The system requirements above.
2. Download sources, in this order: `--mirror` / `DEYROUTE_MIRROR`, then the
   release base built into the installer (none in current builds), then
   GitHub releases. Each source is tried 3 times (waiting 2 s, then 4 s)
   before the next one.
3. `SHA256SUMS` must carry a valid minisign signature of the DEYROUTE release
   key (the public key is inside `install.sh`). The `minisign` tool is used
   when installed, otherwise OpenSSL 3.
4. The archive must match its line in `SHA256SUMS`.
5. The new binary must run on this server before it replaces the old one.
   The replacement is atomic, and the previous binary is kept as
   `/var/lib/deyroute/bin/deyroute.prev`.

Only DEYROUTE release files are downloaded and nothing is sent anywhere.
Failures: `DEY-I004` (every source failed: use `--mirror` or `--local`),
`DEY-I005` (checksum mismatch: corrupt or tampered file), `DEY-I006`
(signature invalid: use the official files; `--skip-signature` is for
testing only).

## Running the installer again

Running the install line on a server that is already set up prints
`existing installation found: repairing/upgrading (config untouched)`,
replaces the binary, restarts `deyroute-hub` or `deyroute-node` if it was running,
and ignores `join`, `--role` and `--name`. Tunnel processes are separate
systemd units and keep running. Use it to repair a broken binary or to move to
another release (`--version V`).

To set a server up from scratch, remove it first with `deyroute uninstall`;
`deyroute setup` and `deyroute join` refuse to overwrite an existing setup
(`DEY-I013`).

## Files and permissions

| Path | Mode / owner | Contents |
| --- | --- | --- |
| `/usr/local/bin/deyroute` + link `dey` | 0755 root | the program |
| `/etc/deyroute/` | 0710 root:deyroute | configuration directory |
| `/etc/deyroute/config.yaml` | 0600 root | the only configuration file |
| `/etc/deyroute/secrets/` | 0700 root, files 0600 | CA, keys, tokens, certificates |
| `/etc/deyroute/backends/` | 0750 root:deyroute | configs rendered from `config.yaml` (never edit by hand) |
| `/var/lib/deyroute/` | 0750 root:deyroute | `state.db` and other state |
| `/var/lib/deyroute/bin/` | 0755 root | backend binaries, one directory per version, and `deyroute.prev` |
| `/var/lib/deyroute/backups/` | 0700 root | backups and `auto/` |
| `/var/log/deyroute/` | 0750 root:deyroute | logs; `tunnels/` holds one log per tunnel |
| `/run/deyroute/daemon.sock` | 0600 root | local API of the menu and the CLI (only root can use them) |
| `/etc/sysctl.d/99-deyroute.conf` | | kernel profile; old values in `/var/lib/deyroute/sysctl-before-deyroute.conf` |

The installer also creates the system user `deyroute` (no login shell). Tunnel
backends run as this user; only WireGuard transports run as root.

systemd services: `deyroute-hub.service` (hub), `deyroute-node.service` (node) and
`deyroute-tun@<tunnel>.<node>.<backend>-<transport>.service` (one per warm
transport).

## Firewalls outside DEYROUTE

DEYROUTE manages only its own nftables table (`inet deyroute`, see
[Security](security.md)). If your provider has a firewall panel, or you use
ufw/firewalld, allow:

- on the hub: the tunnel ports (e.g. 443, 2053) from everyone; the control
  port (44433/tcp) and the backend ports 30000–31999 (tcp and udp) from the
  node IPs;
- on the nodes: 30000–31999 (tcp and udp) from the hub IP (transports where
  the hub connects to the node: `xray/reality`, `hysteria2/udp`,
  `wireguard/kernel`, `direct/*`).

`deyroute port check <port>` shows whether an external firewall blocks a tunnel
port and prints the exact command to open it.
