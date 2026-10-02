<div align="center">

```text
 ██████╗ ███████╗██╗   ██╗██████╗  ██████╗ ██╗   ██╗████████╗███████╗
 ██╔══██╗██╔════╝╚██╗ ██╔╝██╔══██╗██╔═══██╗██║   ██║╚══██╔══╝██╔════╝
 ██║  ██║█████╗   ╚████╔╝ ██████╔╝██║   ██║██║   ██║   ██║   █████╗
 ██║  ██║██╔══╝    ╚██╔╝  ██╔══██╗██║   ██║██║   ██║   ██║   ██╔══╝
 ██████╔╝███████╗   ██║   ██║  ██║╚██████╔╝╚██████╔╝   ██║   ███████╗
 ╚═════╝ ╚══════╝   ╚═╝   ╚═╝  ╚═╝ ╚═════╝  ╚═════╝    ╚═╝   ╚══════╝
```

### A tunnel between your Iran server and your foreign servers that is simple to run, hard to spot, and repairs itself when filtering changes.

**English** · [فارسی](README.fa.md)

[Quick start](#quick-start-5-minutes) ·
[Everyday use](#everyday-use) ·
[How it survives filtering](#how-it-survives-filtering) ·
[Troubleshooting](#something-is-wrong) ·
[Full guides](docs/en/index.md)

</div>

---

## What is DEYROUTE?

DEYROUTE connects two kinds of servers:

- the **hub**: your server **in Iran**. Your users connect to it, and only to it.
- the **nodes**: your servers **abroad**. They run your real VPN service (Xray, Marzban, 3x-ui, and so on).

```text
  Your users                 HUB (Iran)                         NODE (abroad)
  ┌────────┐   HUB_IP:443   ┌──────────────┐   the tunnel    ┌──────────────────┐
  │ phone, │ ─────────────▶ │  deyroute hub  │ ═══════════════▶│  deyroute node     │ ──▶ Xray / Marzban
  │ laptop │                └──────────────┘                 │  your VPN service│     3x-ui ...
  └────────┘                       ▲                         └──────────────────┘
                                   └─ a backup node takes over by itself if this one fails
```

Your users never learn the address of the foreign server. Their traffic travels
through the tunnel exactly as it left their device: DEYROUTE does not open,
change, or re-encrypt it.

## Why people like it

| | |
| --- | --- |
| **Three steps, copy and paste** | Install on the hub, paste one line on the node, create the tunnel. Every question has a suggested answer: press <kbd>Enter</kbd> to take it. |
| **Hard to identify** | Eight different tunnel methods from different programs, including ones that look like ordinary HTTPS to a real website. When one is recognised and blocked, the next one starts. |
| **It fixes itself** | The hub checks every tunnel several times per minute. A blocked method is replaced, normally within 35 seconds, with nobody awake. When the first method works again, the tunnel returns to it. |
| **Backup nodes** | Add a second foreign server and the tunnel moves to it if the first one goes down. |
| **Your service stays as it is** | Same port, same certificate, same UUID. In your client configs only the server address changes, to the hub's IP. |
| **Errors you can read** | Every problem says what happened, why, and what to do, with a fixed code such as `DEY-P012`. |
| **Private by design** | One signed program, no telemetry, no secrets in logs, and exactly one firewall table that DEYROUTE owns. |

## Quick start (5 minutes)

**You need:** a server in Iran (the hub), a foreign server that already runs your
VPN service (the node), and `root` on both. Ubuntu 22.04 / 24.04 / 26.04 or
Debian 12 / 13, on amd64 or arm64. Nothing else: no Python, no Docker.

### 1. On the hub (Iran server)

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh)
```

A short wizard asks five things, one per screen. Type `1` for the role (hub),
give the server a name such as `ir-1`, and press <kbd>Enter</kbd> for the rest.
At the end it prints a **join command**. Copy that whole line.

### 2. On the node (foreign server)

Paste the join command. It is valid once, for 15 minutes:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) join 'dey://…'
```

In about 30 seconds the node shows up on the hub:

```bash
deyroute node list
```

### 3. On the hub: create the tunnel

Use the node id that `deyroute node list` showed, and the port your VPN service uses:

```bash
deyroute tunnel add --node de-1 --ports 443
```

You will see the steps go by, ending with a line like:

```text
✔ Tunnel main is UP via backhaul/wssmux (41ms)
```

### 4. Check it, then change the address in your clients

```bash
deyroute status
deyroute port check 443
```

In your client configs replace the node's address with the **hub's IP**. Keep
everything else (port, UUID, SNI, path) exactly as it was. Done.

> **Recommended next step:** add a second foreign server the same way, then make
> it the backup: `deyroute tunnel backup add main --node nl-1`.

## Everyday use

Type `deyroute` (or just `dey`) to open the menu. Press a number, then
<kbd>Enter</kbd>. <kbd>q</kbd> goes back and <kbd>?</kbd> explains the screen you are on.

```text
 1) Dashboard       4) Ports           7) Optimize         10) Backup & Restore
 2) Tunnels         5) Failover        8) Security         11) Update
 3) Nodes           6) Diagnostics     9) Notifications    12) Settings
```

Everything in the menu is also a command, handy for scripts (add `--json`):

| I want to… | Run |
| --- | --- |
| see everything at a glance | `deyroute status` |
| add a foreign server | `deyroute node join-command` (on the hub), then paste the line on the new server |
| create a tunnel | `deyroute tunnel add --node de-1 --ports 443,2053` |
| add or remove a port | `deyroute port add main 8443` / `deyroute port remove main 8443` |
| check that a port really works | `deyroute port check 443` |
| watch the logs | `deyroute logs main -f` |
| switch method or node by hand | `deyroute tunnel switch main --transport backhaul/tcpmux` |
| add a backup node | `deyroute tunnel backup add main --node nl-1` |
| test every method and compare speed | `deyroute tunnel test-ladder main` |
| get Telegram alerts | menu `9) Notifications` |
| collect a report for support | `deyroute doctor` (secrets are removed) |

New to it? Stay in **Simple mode**: it hides everything advanced and asks at most three
questions per task. Switch in `12) Settings → UI mode` when you want more.

## How it survives filtering

Filtering keeps changing, so one method is never enough. Every tunnel gets a
**ladder** of methods from different programs. One rung carries the traffic; the
others are installed, configured and waiting.

| Rung | Method | Why it is there |
| :-: | --- | --- |
| 1 | `backhaul/wssmux` | TLS and WebSocket in few connections: looks like normal HTTPS |
| 2 | `backhaul/tcpmux` | no TLS layer, least overhead, for when the tunnel's TLS is what gets noticed |
| 3 | `rathole/noise` | a different program and protocol with Noise encryption |
| 4 | `frp/tcp` | a third program with a different traffic pattern |
| 5 | `xray/reality` | looks like a real TLS 1.3 visit to a harmless website |
| 6 | `hysteria2/udp` | QUIC: fastest on lossy links, when UDP is open |
| 7 | `waterwall/reverse-reality` | a reverse connection that also looks like a real website |
| 8 | `direct/native` | no disguise; only so the service does not go down |

When the hub confirms that the active rung is blocked, it starts the next one;
users usually do not notice. When rung 1 recovers, the tunnel goes back. You can
reorder the ladder or build your own in the menu (`5) Failover`) or with
`deyroute ladder`. Details: [the filtering FAQ](docs/en/faq-filtering.md).

**Good habits that keep your servers off the radar**

- Give users only the hub's address. Never publish the foreign server's IP.
- Keep your service's own TLS settings (Reality, WebSocket, and so on). DEYROUTE does not replace them, it carries them.
- Set three decoy sites for the Reality rungs: `deyroute config edit`, then `hub.decoy_snis`.
- Keep a backup node in another provider or country.
- No tool can promise to be invisible. DEYROUTE's job is to make recognising and blocking you expensive, and to recover quickly when it happens.

## Keeping it safe

- The foreign servers always connect **out** to the hub, over mutual TLS 1.3 with a private CA. The hub never logs in to a node, and SSH is not used.
- The join line works **once** and expires in 15 minutes.
- DEYROUTE creates and manages exactly one firewall table (`inet deyroute`) and never touches your other rules.
- Every release is signed; the installer verifies the signature and checksums before installing.
- No telemetry. Secrets are stored with mode `0600` and never written to logs.

More: [security guide](docs/en/security.md).

## Update, back up, move, remove

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh)   # same line = repair or upgrade
deyroute update                      # update deyroute and the backends without dropping tunnels
deyroute backup                      # encrypted backup of the hub
deyroute restore FILE                # restore it on a new server
deyroute node set-hub NEW_IP:44433   # after moving the hub, point a node at it
deyroute uninstall                   # remove everything and restore the system
```

Guides: [update and uninstall](docs/en/update-uninstall.md) ·
[backup and restore](docs/en/backup.md) · [moving the hub](docs/en/hub-move.md).

## Something is wrong?

Start with `deyroute doctor`. It checks the system, ports, certificates, units and
logs, and explains each finding in plain words.

| Symptom | Try |
| --- | --- |
| a node is `offline` | `deyroute node test <id>`, then check the control port 44433 in your provider's firewall |
| the tunnel is `UP` but the client cannot connect | `deyroute port check <port>`: the provider's firewall often closes the hub port |
| the tunnel keeps switching methods | `deyroute logs <tunnel>` and `deyroute tunnel test-ladder <tunnel>` |
| an error with a code | read its three lines (what / why / fix); all codes are in [docs/ERRORS.md](docs/ERRORS.md) |

More: [troubleshooting](docs/en/troubleshooting.md).

## Good to know

- **Hub and node are the same program.** The role is chosen in the wizard.
- **Your TLS is never touched.** The tunnel carries raw bytes, so your client keeps its SNI, path, UUID and fingerprint.
- **Both servers need to reach each other.** If the network between the two countries cuts the direct path, tell us by opening an issue; a way around that is being built.
- **Development builds.** Releases are currently `edge` builds, tested with real systemd, nftables and every backend ([acceptance evidence](docs/en/acceptance.md)). Running the install line again upgrades in place and keeps your configuration.

## Documentation

| Guide | |
| --- | --- |
| [Install](docs/en/install.md) | requirements, offline install, mirrors, what the installer verifies |
| [Join a node](docs/en/join.md) | the join line, expiry, IP changes |
| [First tunnel](docs/en/first-tunnel.md) | the menu and the CLI, port syntax, health probes |
| [Backup node](docs/en/backup-node.md) | failover, failback, timings |
| [Filtering FAQ](docs/en/faq-filtering.md) | the ladder, skipped methods, decoy sites |
| [Troubleshooting](docs/en/troubleshooting.md) | doctor, logs, common codes |
| [Security](docs/en/security.md) | control channel, tokens, firewall, rotation |
| Reference | [error codes](docs/ERRORS.md) · [`--json` output](docs/cli-json.md) · [backends](docs/backends/) |
| Project | [architecture](docs/dev/ARCHITECTURE.md) · [releasing](docs/dev/RELEASING.md) · [decisions and open questions](QUESTIONS.md) · [changelog](CHANGELOG.md) |

## Build from source

```bash
make build        # static binary in ./dist (CGO_ENABLED=0)
make test lint    # unit tests and linters
```

Integration scenarios run in systemd containers: `test/integration/run.sh`
(see the header of the script). Issues and pull requests are welcome.
