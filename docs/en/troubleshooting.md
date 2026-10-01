# Troubleshooting

[فارسی](../fa/troubleshooting.md) · [Index](index.md)

## Start here

```bash
deyroute status                  # is the tunnel UP? is the node online?
deyroute doctor                  # 15 automatic checks + a support file
deyroute port check 443          # the four-stage port check
deyroute logs main -f            # live tunnel log, hub and node side
deyroute events --tunnel main    # what happened in the last 24 hours
```

The same tools are in the menu under `6) Diagnostics`: `1) Port check`,
`2) Tunnel test`, `3) Speed test`, `4) Logs`, `5) Doctor`.

## Reading an error

Every error has a fixed code `DEY-<letter><3 digits>` and is shown the same
way in the menu and on the command line:

```text
✖ DEY-P012  Port 443/tcp is already in use
  Why:  nginx (pid 1234) is listening on 0.0.0.0:443
  Fix:  choose another port, or stop that service first
  Log:  /var/log/deyroute/hub.log (search DEY-P012)
```

Line 1 says what happened, `Why` why, `Fix` what to do, `Log` where to read
more. Lines starting with `|` below it are details (for example the last
lines of a backend log). Copy the whole block when you ask for help. The full
list of codes with Why and Fix is in [ERRORS.md](../ERRORS.md).

| Letter | Area |
| --- | --- |
| `I` | installer, setup, environment |
| `C` | configuration (`config.yaml`) |
| `N` | nodes and the control channel |
| `P` | ports and firewall |
| `T` | TLS certificates |
| `B` | backends (the tunnel programs) |
| `F` | failover |
| `S` | security, backups, updates |
| `X` | internal / system |

Exit codes of the command line: `0` success, `1` user error, `2` system error
(`DEY-X…`), `3` confirmation needed (add `--yes` when running without a
terminal).

## `deyroute doctor`

```bash
deyroute doctor                   # this server
deyroute doctor --node de-1       # this hub plus node de-1
deyroute doctor --out /tmp/doctor.tar.gz
```

Doctor collects the system and versions, the state of every unit, the last
lines of every log, the ports and the result of the port check, the firewall
table, kernel settings, nodes and control RTT, the ladder probes, recent
events and certificate expiry. It runs 15 checks and prints a short summary in
plain language, for example:

- `Node de-1 is offline on the control channel, but tunnel main still passes traffic: the control network has a problem, the tunnel is healthy`
- `Tunnel main is DOWN and every transport failed or is quarantined: the IP of node de-1 is probably blocked`
- `Tunnel main is connected, but the service behind it on node de-1 does not answer on 127.0.0.1:443`
- `Clock of de-1 differs from the hub by …: TLS connections fail with a large difference`

Each finding comes with a `Fix:` line. Doctor also writes a support file,
`/root/deyroute-doctor-<UTC>.tar.gz`, with **every secret removed** (tokens,
keys and passwords are masked, and the file is checked once more before it is
written). Send the file together with the summary. When the service is not
running, doctor still collects what it can on this server and says so.

## Logs and events

| File | Contents |
| --- | --- |
| `/var/log/deyroute/hub.log` / `node.log` | the hub service / the node agent |
| `/var/log/deyroute/tunnels/<tunnel>.log` | the backend programs of one tunnel |
| `/var/log/deyroute/events.log` | every event, one line each |
| `/var/log/deyroute/deyroute.log` | errors of the `deyroute` command itself |

```bash
deyroute logs main -f            # tunnel: hub and node lines, prefixed [hub] / [node]
deyroute logs hub --since 1h
deyroute logs node               # on a node: its own agent
deyroute events --since 24h --json
```

deyroute's own logs are JSON lines with UTC times; logs are rotated at 20 MB
(5 compressed files kept). For more
detail run a command with `--debug`, or start the service with
`DEYROUTE_DEBUG=1`. Secrets never appear in logs (they are printed as `***`).

Important event types: `tunnel_up`, `tunnel_degraded`, `tunnel_down`,
`switch_transport`, `switch_node`, `failback`, `failback_failed`,
`flapping`, `node_online`, `node_offline`, `node_ip_changed`,
`service_down`, `backend_crash`, `probe_error`, `rung_skipped`,
`rung_restored`, `config_applied`. A manual switch is recorded as
`switch_transport` or `switch_node` with the reason `manual switch to …`.

## Common problems

### The command says `DEY-X003 Daemon not running`

The service behind the menu and the CLI is not running.

```bash
systemctl status deyroute-hub        # on a node: deyroute-node
systemctl start deyroute-hub
journalctl -u deyroute-hub -n 50
```

On a server that was never set up the fix line says so: run `deyroute setup`
or join a hub.

### A port is busy — `DEY-P012`

```text
✖ DEY-P012  Port 443/tcp is already in use
  Why:  nginx (pid 1234) is listening on 0.0.0.0:443
```

Another program uses the port on the hub. Choose another port
(`deyroute port suggest`) or stop that program yourself
(`systemctl stop nginx; systemctl disable nginx`). DEYROUTE never stops a
program that is not its own. `DEY-P011` means the port is reserved (22, the
control port, 30000–31999).

### A firewall blocks the port — `DEY-P013`, `DEY-P014`

`deyroute port check 443` shows which firewall blocks it and the exact command
to open it (for example `ufw allow 443/tcp`); run it yourself. `DEY-P014`
(not reachable from the node) usually means the provider's firewall panel
blocks the port: open it there. See also `deyroute security firewall show`.
`DEY-P031` means DEYROUTE does not manage the firewall on this hub
(`security.firewall_managed: false`); open the ports by hand.

### UDP is blocked — `DEY-P015`, `DEY-B007`

The UDP test between hub and node failed, so transports that need UDP
(`hysteria2/udp`, `wireguard/kernel`, `backhaul/udp`, `frp/quic`, `frp/kcp`)
are skipped for this node. TCP transports keep working. Open UDP between the
servers (provider firewall) if you want them; skipped rungs are re-tested
every 30 minutes. `deyroute node test de-1` shows `UDP ok` or `blocked`.

### The node is offline — `DEY-N003`

- If the tunnel still passes traffic, only the control connection is down:
  the tunnel is fine and nothing switches.
- On the node: `systemctl status deyroute-node` and `deyroute logs node`.
- The node must reach `HUB_IP:44433`; check the hub's provider firewall.
- If the node server is down or its IP is blocked, the tunnel fails over to a
  backup node — add one if you have none ([Backup node](backup-node.md)).
- `DEY-N004`: the versions differ; see [Join](join.md#incompatible-versions).

### The tunnel is DOWN — `DEY-F001`

Every transport on every node failed. The ladder is retried from rung 1 every
30 seconds (up to every 5 minutes). Check:

1. `deyroute node test de-1` — is the node reachable at all?
2. `deyroute tunnel show main` — which rungs are skipped or quarantined?
3. `deyroute logs main` — errors of the backends.
4. Is the service on the node running (`DEY-F005`)?

If every rung times out, the node's IP is probably blocked from the hub:
give the node a new IP, or add a backup node.

### The service on the node is down — `DEY-F005`

The tunnel works but nothing answers behind it on the node (for example Xray
stopped or listens on another port). Start the service
(`systemctl restart xray`) and make sure it listens on the tunnel's target
(`127.0.0.1:443` by default). With a backup node, the tunnel moves there in
the meantime.

### The tunnel is UP but users cannot connect

- The tunnel checks only work from the hub and from the node; they cannot see
  filtering **inside Iran**. If users cannot reach `HUB_IP:443`, the hub IP or
  port may be filtered for them: try another port (e.g. 2053, 8443) or move
  the hub ([Hub move](hub-move.md)).
- Check the client config: only the address changes to the hub IP; port,
  UUID, SNI, host and path stay as on the node.
- `deyroute diag probe main --all-ports` probes every port of the tunnel.

### Switches keep happening — `DEY-F002`

After 6 automatic switches in one hour failover holds the current state. The
network is unstable: read `deyroute events --tunnel main`, pin one transport
(`deyroute tunnel switch main --transport <id>`) or pause failover
(`deyroute tunnel pause main`).

### A backend does not start — `DEY-B003`, `DEY-B004`

`DEY-B003` shows the last lines of the backend's log; `DEY-B004` means it
started but no traffic passed within 15 seconds. Read `deyroute logs main`,
then `deyroute tunnel restart main`, or switch to another rung. `DEY-B001`:
the binary could not be downloaded — a node must be online (the hub downloads
through it), or set `DEYROUTE_MIRROR`.

### Clock difference

TLS fails when a server's clock is far off. Enable time sync on both servers:
`timedatectl set-ntp true`.

## Most common codes

| Code | Meaning | What to do |
| --- | --- | --- |
| `DEY-I001` | not root | run as root |
| `DEY-I004` | download failed from every source | `--mirror URL`, `DEYROUTE_MIRROR` or `--local FILE` ([Install](install.md)) |
| `DEY-I006` | signature invalid | use the official release files |
| `DEY-I013` | server already set up | use the menu, or `deyroute uninstall` before a new setup |
| `DEY-I014` | a setup step failed | read the lines below the error, fix, run the installer again |
| `DEY-I021` | detected IP is not public | enter the real public IP (`hub.public_ip`, then `deyroute config apply`) |
| `DEY-C001` | unknown key in `config.yaml` | remove it; `deyroute config validate` |
| `DEY-C003` | two tunnels use one port | change one tunnel's port |
| `DEY-C014` | `config.yaml` unreadable | restore the last good copy from `/var/lib/deyroute/backups/auto/` |
| `DEY-C021` | unknown tunnel | `deyroute tunnel list` for the ids |
| `DEY-N001` | join token invalid or expired | new join command |
| `DEY-N002` | CA fingerprint mismatch | copy the join command again, do not edit it |
| `DEY-N003` | node offline | see above |
| `DEY-N004` | incompatible versions | same release on hub and node |
| `DEY-N009` | hub unreachable from the node | hub service and provider firewall |
| `DEY-N012` | no online node to download through | bring a node online or set `DEYROUTE_MIRROR` |
| `DEY-P011` | reserved port | another port (`deyroute port suggest`) |
| `DEY-P012` | port in use | another port, or stop that program |
| `DEY-P013` / `P014` | firewall blocks the port | open it (command in the error / provider panel) |
| `DEY-P015` | UDP blocked | nothing needed; UDP rungs are skipped |
| `DEY-T001` / `T006` | certificate expired / expiring | `deyroute security tls renew` |
| `DEY-T003` | ACME failed | domain DNS-only, port 80 free; TLS fell back to `auto` |
| `DEY-B003` | backend unit failed to start | see its log lines, `deyroute tunnel restart <tunnel>` |
| `DEY-B004` | no traffic after start | `deyroute logs <tunnel>`, try the next rung |
| `DEY-B006` / `B007` | rung skipped for this tunnel | re-tested every 30 minutes; see [Filtering FAQ](faq-filtering.md) |
| `DEY-B042` | no decoy SNI reachable | set `hub.decoy_snis` ([Filtering FAQ](faq-filtering.md#decoy-sni)) |
| `DEY-F001` | all candidates failed | see "The tunnel is DOWN" |
| `DEY-F002` | flapping limit | see above |
| `DEY-F003` | failback failed | nothing; it retries later |
| `DEY-F005` | service down on the node | start the service |
| `DEY-S004` | cannot decrypt backup | wrong passphrase |
| `DEY-S008` | backup passphrase required | type it, or set `DEYROUTE_BACKUP_PASSPHRASE` |
| `DEY-X003` | daemon not running | `systemctl start deyroute-hub` (or `deyroute-node`) |
| `DEY-X008` | not implemented yet | this build does not have the feature yet; see `CHANGELOG.md` |
| `DEY-X009` | command for the other role | e.g. tunnel commands run on the hub, `node set-hub` on a node |
| `DEY-X000` | unexpected error | `deyroute doctor` and send the file |
