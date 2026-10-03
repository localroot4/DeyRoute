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
wizard. The wizard asks at most five questions. Each question is a block:
the step and the question on the first line, the numbered answers one per
line, a short explanation, and a last line that says exactly what to type
and what Enter alone takes:

```text
Step 1 of 5 · Role of this server
   Set up the hub first; each node then joins it with the command the hub prints.
   1) hub    the server in Iran: your users connect to it
   2) node   the server abroad: runs your VPN service (Xray, Marzban node ...)
   Type 1 or 2 and press Enter. Enter alone = 1 (hub).
 › 1

Step 2 of 5 · Name of this hub
   Shown in the menu, the logs and the messages. Letters, digits and -, e.g. ir-1.
   Press Enter to use myhost, or type another value and press Enter.
 › ir-1

Step 3 of 5 · Public IP of this server
   The address your users and your nodes connect to.
   Detected automatically: 5.6.7.8
   Press Enter to use 5.6.7.8, or type another value and press Enter.
 ›

Step 4 of 5 · Control port for the nodes
   Your nodes connect to the hub on this TCP port (mutual TLS). Allow it in your provider's firewall too.
   Press Enter to use 44433, or type another value and press Enter.
 ›

Step 5 of 5 · Tune this server automatically (recommended)
   deyroute measured this server and lists the kernel settings that suit it:
   Measured: 2.0 GiB RAM · 2 CPU · kernel 6.1.0-21-amd64 · eth0 MTU 1500
   9 settings change (4 of them only after a reboot or the next start):
     net.ipv4.tcp_congestion_control                     cubic   → bbr
     net.core.somaxconn                                  4096    → 65535
     net.core.rmem_max                                   212992  → 33554432
     …
     net.netfilter.nf_conntrack_max                      -       → 131072  (after reboot)
   Why each one: deyroute optimize auto --dry-run (after setup). You can undo it any time with: deyroute optimize revert
   Type y (yes) or n (no) and press Enter. Enter alone = y (yes).
 ›

Summary
   Role             hub
   Name             ir-1
   Public IP        5.6.7.8
   Control port     44433
   Kernel profile   automatic (9 changes)
```

- **Public IP**: detected automatically. If the detected address is private or
  CGNAT, the wizard says so; type the address users connect to (see your
  provider panel).
- **Control port**: the port the nodes connect to. `44433` by default; when it
  is taken, the next free port is offered.
- **Automatic tuning**: before the question the wizard measures the server
  (RAM, CPUs, kernel, network card, conntrack) and lists every kernel setting
  it would change, with the current and the new value. *Yes* writes
  `/etc/sysctl.d/99-deyroute.conf` with that plan (profile `auto`: the
  balanced values plus buffers sized to the RAM, raise-only limits, the
  reserved backend ports and, when needed, a conntrack table that fits). The
  previous values are saved and `deyroute optimize revert` restores them.
  *No* leaves the kernel alone (profile `off`). In a container (OpenVZ, LXC,
  Docker) the kernel belongs to the host, so every kernel item is skipped
  (`DEY-X064`). Every step, why it is safe and how it is undone is in
  [Automatic tuning](tuning.md).
- Later, `deyroute optimize auto` (menu `7) Optimize → 1) Automatic tuning`)
  plans and applies the same on the hub and every online node after one
  confirmation, and `deyroute optimize check` reports values someone else
  changed. The fixed profiles stay available: `deyroute optimize apply
  --profile P` applies `balanced` or `aggressive` on the hub and every online
  node with the hub's `tuning.bbr`; what a node skips is shown as
  `node <id>: …`. `aggressive` (64 MB buffers) is meant for servers with
  4 GB RAM or more: the menu names the profile recommended for the hub's RAM
  and warns before `aggressive` on a smaller hub.

Everything else is automatic. The wizard shows each step:

```text
  ✔ Detect public IP
  ✔ Create internal CA
  ✔ Issue hub certificate
  ✔ Apply firewall (table inet deyroute)
  ✔ Apply kernel settings
  ✔ Write /etc/deyroute/config.yaml
  ✔ Install and start service

✔ Hub ir-1 is ready: 5.6.7.8, control port 44433.

Next: add a node (the server abroad that runs your VPN service)
   1. Run this command on the node (one node per command, valid until 12:15, 15m):

bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) join 'dey://…@5.6.7.8:44433#sha256:…' --version 1.0.0

   2. When the node is online (deyroute node list), create a tunnel on this hub:
      in the menu: run deyroute, then 2) Tunnels → 1) Add tunnel
      or directly: deyroute tunnel add --node <node id> --ports 443
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
free one) and the server is tuned automatically (profile `auto`). The same flags work on
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

`SHA256SUMS` and `SHA256SUMS.minisig` must be next to the archive: without
them nothing is installed (`DEY-I005` / `DEY-I006`).

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
replaces the binary, then runs `deyroute setup --repair`: missing directories
and unit files are restored and `deyroute-hub` or `deyroute-node` is enabled and
restarted. `join`, `--role` and `--name` are ignored. Tunnel processes are
separate systemd units and keep running. Use it to repair a broken
installation or to move to another release (`--version V`).

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
port and prints the exact command to open it; with `--open` deyroute runs that
command after you confirm it.
