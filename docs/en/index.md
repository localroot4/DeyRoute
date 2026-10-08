# DEYROUTE Tunnel Manager — documentation

[فارسی](../fa/index.md)

DEYROUTE builds a censorship-resistant tunnel between an Iran server (the
**hub**) and one or more foreign servers (the **nodes**). Users connect to
`HUB_IP:PORT`; their traffic reaches the real service on the node (Xray,
Marzban, 3x-ui …) byte for byte, without the tunnel touching its TLS. When
filtering blocks the current transport, DEYROUTE switches to the next one on
its own, and to a backup node when a node fails.

## Quick start (5 minutes)

You need: an Iran server for the hub, a foreign server that already runs your
VPN service (for example Xray on port 443), and root on both. Supported:
Ubuntu 22.04/24.04/26.04 and Debian 12/13 (amd64 or arm64); see
[Install](install.md) for the full list.

1. **On the hub**, as root:

   ```bash
   bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh)
   ```

   Choose `1` (hub), name it (e.g. `ir-1`) and press Enter for the other
   questions. The last lines show a **join command**.

2. **On the node**, as root, paste that join command. It looks like this and
   is valid once, for 15 minutes:

   ```bash
   bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) join 'dey://TOKEN@HUB_IP:44433#sha256:…' --version 1.0.0
   ```

   Within about 30 seconds the node shows up on the hub (`deyroute node list`).

3. **On the hub**, create the tunnel (use the node id that `deyroute node list`
   shows):

   ```bash
   deyroute tunnel add --node de-1 --ports 443,2053 --name main
   ```

   Or open the menu with `deyroute` (or `dey`) and choose `2) Tunnels` →
   `1) Add tunnel`. The tunnel starts on the first rung of the default ladder
   and ends with a line like `Tunnel main is UP via backhaul/wssmux (41ms)`.

4. **Check it:**

   ```bash
   deyroute status
   deyroute port check 443
   ```

5. **In your client configs**, replace the node's address with the hub's IP.
   Keep everything else (port, UUID, SNI, host, path).

Optional, but recommended: join a second node and make it a backup:
`deyroute tunnel backup add main --node nl-1` ([Backup node](backup-node.md)).

## Pages

| Page | What it covers |
| --- | --- |
| [Install](install.md) | requirements, hub install, non-interactive and offline install, mirrors, what the installer verifies, re-run = repair/upgrade, files |
| [Join a node](join.md) | join command, TTL, what happens on the node, IP changes, incompatible versions |
| [First tunnel](first-tunnel.md) | menu and CLI walk-through, port syntax, progress steps, checking that it works, logs |
| [Backup node](backup-node.md) | backup nodes, how failover and failback work, timings, warm rungs, manual switch/pause/reset/test |
| [Troubleshooting](troubleshooting.md) | doctor, logs, events, the error format, the most common DEY codes and what to do |
| [Filtering FAQ](faq-filtering.md) | the transport ladder, skipped rungs, decoy SNI, why TLS is not touched, client IP, what DEYROUTE cannot do |
| [Hub move](hub-move.md) | moving the hub to a new server or IP without re-joining the nodes |
| [Through Cloudflare](front.md) | when the direct path between hub and node is filtered: the node and its tunnels go through Cloudflare (`deyroute front enable`) |
| [Backup & restore](backup.md) | backups, passphrase, `DEYROUTE_BACKUP_PASSPHRASE`, automatic backups, what a backup holds |
| [Update & uninstall](update-uninstall.md) | updating deyroute and the backends, rollback, uninstall |
| [Security](security.md) | control channel, join tokens, firewall table, secrets, rotation, TLS, audit, no telemetry |
| [Traffic and load](monitoring.md) | what is counted, download/upload directions, the TRAFFIC block, the Traffic screen, `deyroute stats`, history, monthly quota, not in backups |
| [Automatic tuning](tuning.md) | `optimize auto`: what is measured, every change and why it is safe, conntrack, containers, check, undo |
| [Benchmarks](benchmarks.md) | how resource use and failover times are measured; lab results recorded, real-server results pending |

Reference: [error codes](../ERRORS.md) · [`--json` output](../cli-json.md) ·
backends: [backhaul](../backends/backhaul.md), [rathole](../backends/rathole.md),
[frp](../backends/frp.md), [xray](../backends/xray.md),
[hysteria2](../backends/hysteria2.md), [waterwall](../backends/waterwall.md),
[wireguard / awg](../backends/wireguard.md), [direct](../backends/direct.md),
[gost](../backends/gost.md), [chisel](../backends/chisel.md).

## Words used in these pages

| Word | Meaning |
| --- | --- |
| Hub | the Iran server; users connect to it; it runs the menu, the database and the failover engine |
| Node | a foreign server; it runs your real VPN service and the other end of the tunnel |
| Tunnel | one path from the hub to a node, with one or more ports |
| Transport | one tunnel method, such as `backhaul/wssmux` or `rathole/noise` |
| Ladder / rung | the ordered list of transports of a tunnel; each entry is a rung |
| Failover / failback | the automatic switch after a confirmed failure / the automatic return to rung 1 |
| Warm | installed, configured and ready, but stopped (uses no memory) |

## The menu

`deyroute` or `dey` without arguments opens the menu. Type a number and press
Enter; `q` or Esc goes back, `r` refreshes, `?` shows help for the current
screen. Items marked `*` appear only in Advanced mode
(`12) Settings` → `1) UI mode (Simple / Advanced)`).

When a question waits for typed text, `q` and `r` are part of the answer:
Enter accepts (an empty answer keeps the value in brackets), Esc cancels and
`?` on an empty answer shows the help; the footer says so. Questions with
fixed answers (policy, TLS mode, ladder profile, backup node) are numbered
lists with the current value marked `(current)`. On a terminal without
cursor control (`TERM=dumb`) the menu runs in line mode: each line you type
is one answer and an empty line is Enter.

```text
 1) Dashboard (live)
 2) Tunnels        add / edit / enable-disable / restart / switch transport / delete
 3) Nodes          show join command / list / rename / remove / test
 4) Ports          add port to tunnel / remove / check port / firewall status
 5) Failover       policy / ladder order / backup nodes / thresholds *
 6) Diagnostics    port check · tunnel test · speed test · logs · doctor
 7) Optimize       sysctl profile · BBR · limits *
 8) Security       rotate tokens · TLS · firewall · view fingerprints *
 9) Notifications  Telegram bot setup / test message
10) Backup & Restore
11) Update         deyroute / backends / manifest
12) Settings       ui mode (Simple/Advanced) · language · uninstall
 0) Exit
```

On a node server the menu keeps these numbers. Items only the hub can do
are marked `(hub only)`; picking one says that it is managed in the hub's
menu. A node has `1) Dashboard`, `6) Diagnostics` (logs, doctor),
`10) Backup & Restore` (with `3) Set hub address` after a
[hub move](hub-move.md)) and `12) Settings` (uninstall).

Everything the menu does is also a command. `deyroute --help` lists them and
`deyroute <command> --help` shows flags and examples. Every command accepts
`--json` for scripts ([schema](../cli-json.md)) and `--debug` (or
`DEYROUTE_DEBUG=1`) for debug logging.

Exit codes: `0` success, `1` user error (`DEY-C/P/T/N/B/F/S/I` codes, wrong
arguments, a declined confirmation), `2` system error (`DEY-X`), `3` a
destructive command needs `--yes` because no terminal is attached.

## Where things are

| Path | Contents |
| --- | --- |
| `/usr/local/bin/deyroute`, `/usr/local/bin/dey` | the program and its short name |
| `/etc/deyroute/config.yaml` | the only configuration file (0600) |
| `/etc/deyroute/secrets/` | keys, tokens and certificates (0700, files 0600) |
| `/var/lib/deyroute/state.db` | live state, probe history, events |
| `/var/lib/deyroute/backups/` | backups; `auto/` holds the automatic ones |
| `/var/log/deyroute/` | `deyroute.log`, `hub.log` or `node.log`, `events.log`, `tunnels/<tunnel>.log` |
| systemd units | `deyroute-hub.service`, `deyroute-node.service`, `deyroute-tun@….service` (one per warm transport) |
