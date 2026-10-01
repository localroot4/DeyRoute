# Your first tunnel

[فارسی](../fa/first-tunnel.md) · [Index](index.md)

A tunnel forwards one or more ports of the hub to a node. Users connect to
`HUB_IP:443`; the bytes arrive at `127.0.0.1:443` on the node, where your VPN
service (Xray, Marzban, 3x-ui …) answers exactly as before.

## Before you start

- At least one node has joined (`deyroute node list` shows it `online`).
- The service on the node listens on the port you want to forward, on
  `127.0.0.1` or `0.0.0.0` (the tunnel delivers to `127.0.0.1:<port>` by
  default).
- The port is free on the hub. Ports 22, the control port (44433) and
  30000–31999 are reserved (`DEY-P011`); `deyroute port suggest` lists free
  ports, Cloudflare-compatible ones first (443, 2053, 2083, 2087, 2096, 8443).

## With the menu

Open `deyroute` and choose `2) Tunnels` → `1) Add tunnel`. In Simple mode the
wizard asks at most three questions:

1. **Which node?** — a numbered list of online nodes. With only one node it is
   chosen for you: `Only one node is online: de-1. It is used for this tunnel.`
2. **Ports?** — for example `443,2053`. Each port is checked at once:
   `443/tcp is free`, or `443/tcp is used by nginx`. For a busy port choose
   `Change port`, `Skip`, or — only when a deyroute tunnel holds it —
   `Stop that service`. deyroute never stops other programs.
   The wizard also stops at a free port that the host firewall (ufw,
   firewalld, iptables or another nftables table) closes, or that the node
   cannot reach: `Change port`, `Skip`, `Open it in the firewall:
   ufw allow 443/tcp` (shown only when deyroute knows the command) or `Keep it
   and continue`. Opening shows the exact command(s) and runs them on the hub
   only after you type `yes`; the port is then checked again. A port the
   node cannot reach is usually closed in the provider's firewall panel,
   which deyroute cannot change.
3. **Confirm** — a summary (node, ports, ladder, backup). Enter creates the
   tunnel.

A progress screen follows:

```text
  ✔ install backend on hub
  ✔ install on node
  ✔ render
  ✔ firewall
  ✔ start
  ✔ probe
  Tunnel main is UP via backhaul/wssmux (41ms)
```

If a step fails you see its code with Why and Fix and can choose
`1) Retry` or `0) Back`.

Advanced mode (`12) Settings` → `1) UI mode`) adds optional questions: the
tunnel name, a custom target per port, the probe kind of each TCP port (see
[below](#how-the-health-probe-tests-a-port)), a backup node, the ladder
order, the TLS mode and the failover thresholds.

## With the command line

```bash
deyroute tunnel add --node de-1 --ports 443,2053 --name main
```

On a terminal it first shows a summary and asks `Create the tunnel? [Y/n]`;
`--yes` creates at once. Then it prints the same steps as the menu.

| Flag | Meaning |
| --- | --- |
| `--node <id>` | primary node (required) |
| `--ports …` | listen ports (required), see the table below |
| `--name N` | display name; the tunnel **id** is derived from it (`main`) |
| `--ladder default` | a ladder profile, or an inline list such as `backhaul/wssmux,rathole/noise` |
| `--backup <id>` | backup node(s); repeat the flag or separate ids with commas |
| `--yes` | do not show the summary first |

The tunnel id is what all other commands use (`deyroute tunnel show main`).
Without `--name` the id is `tunnel` (then `tunnel-2`, …); `deyroute tunnel list`
shows it.

More examples:

```bash
deyroute tunnel add --node de-1 --ports 443,27015/udp --name "Main" --backup nl-1 --yes
deyroute tunnel add --node de-1 --ports 443 --ladder backhaul/wssmux,rathole/noise
```

### Port syntax

| Input | Meaning |
| --- | --- |
| `443` | TCP 443 on the hub → `127.0.0.1:443` on the node |
| `443/udp` | UDP 443 |
| `443,2053,8443` | several ports |
| `443/tcp,27015/udp` | mixed TCP and UDP |
| `2000-2010` | a range (each port becomes one port map; `2000-2010/udp` for UDP) |
| `443:8443` | listen on 443, deliver to `127.0.0.1:8443` on the node |
| `443:10.0.0.5:8443` | deliver to another address on the node's side |

A tunnel holds at most 64 port maps (`DEY-C015`, `DEY-P016`). A range counts
one port map per port, so `2000-2100` (101 ports) does not fit in one tunnel:
split it over two tunnels, e.g. `2000-2063` and `2064-2100`. Two tunnels
cannot use the same port and protocol (`DEY-C003`).

## What happens when you create a tunnel

1. The ports are checked and an automatic backup of `/etc/deyroute` is taken
   (`/var/lib/deyroute/backups/auto/`).
2. The tunnel is written to `config.yaml`.
3. The backend binaries are installed on the hub and on the node (the hub
   downloads through the node when it cannot reach GitHub), every rung of the
   ladder is rendered and **warmed** (ready but stopped), the firewall is
   opened and the tunnel's own TLS certificate is created.
4. Rung 1 is started and probed; the first successful probe makes the
   tunnel `UP`.

When a step fails, the error is shown and the tunnel stays configured and
keeps trying by itself. Fix the cause and run `deyroute tunnel restart main`.

A tunnel with only UDP ports (for example a game server) uses its own ladder
(`udp-default`); rungs that cannot carry the tunnel's protocols are left out
automatically. See [Filtering FAQ](faq-filtering.md).

## Check that it works

```bash
deyroute status
deyroute tunnel show main
deyroute diag probe main --all-ports
deyroute port check 443
```

- `status` is the dashboard: every tunnel with its active node, transport,
  state (`UP`, `DEGR`, `SWITCHING`, `DOWN`, `DISABLED`, `PAUSED`), RTT, up-time
  and ports; every node; the last events. `deyroute status --watch` refreshes
  every 2 seconds.
- `tunnel show` lists the ports, the ladder, the failover settings, whether
  the client IP is `preserved` or `masked`, every rung (`active`, `warm`,
  `skipped: …`, `quarantined until …`) and the recent probe results.
- `diag probe` probes the tunnel's ports now.
- `port check` runs the four-stage check:

  ```text
  Port 443/tcp
    1. local bind              ✖ used by backhaul … (a deyroute unit)
    2. firewall                ✔ open (nftables)
    3. reachable from node     ✔ de-1: yes (39ms)
    4. reachable via tunnel    ✔ main: yes (41ms)
    Note: this shows the port is open from the internet; it does not measure filtering inside Iran.
  ```

  For a port that already belongs to a running tunnel, line 1 shows the
  deyroute unit that holds it; that is expected. For a new port it should say
  `free`. Line 3 is tested by a node from outside Iran — it proves the port
  is open on the internet, not that it is reachable from inside Iran.

  When line 2 says `closed (ufw); open it with: ufw allow 443/tcp`,
  `deyroute port check 443 --open` lets deyroute run that exact command: it
  shows it, asks you to type `yes` (`--yes` skips the question; without a
  terminal and without `--yes` it stops with exit code 3), and checks the
  firewall again. If a second firewall still closes the port, its command is
  shown and confirmed the same way. In the menu: `4) Ports` → `Check port`,
  then `1) Open it in the firewall`.

Finally test with a real client: in the client config change only the server
address to the hub's IP. Keep the port, UUID/password, SNI, host header,
path and fingerprint as they are — the tunnel does not touch TLS, so the
node's certificate and settings are what the client sees.

## Logs and events

```bash
deyroute logs main -f           # tunnel log, hub and node side ([hub] / [node]), live
deyroute logs hub --since 1h    # the hub service
deyroute events --tunnel main   # switches, failures, failbacks (last 24 h)
```

Without `--since`, `logs` prints the last 200 lines. Secrets are always
masked. The files are in `/var/log/deyroute/` (`tunnels/main.log` for this
tunnel). In the menu: `6) Diagnostics` → `4) Logs`.

## Change the tunnel later

```bash
deyroute port add main 8443                           # add a port
deyroute port add main 8443/tcp --target 127.0.0.1:9443
deyroute port add main 8080 --probe http              # with its probe kind (below)
deyroute port set main 443 --probe tls                # change the probe kind
deyroute port remove main 8443
deyroute tunnel edit main --name "Main 443"
deyroute tunnel edit main --probe-port 2053           # which port the health probe uses
deyroute tunnel restart main
deyroute tunnel disable main                          # stops forwarding (asks for yes)
deyroute tunnel enable main
deyroute tunnel delete main                           # asks for yes
```

Adding or removing a port restarts the active transport (an interruption of
up to 3 seconds); changing a probe kind restarts nothing. Deleting a tunnel
removes its units, firewall rules, secrets and probe history; its events are
kept.

### How the health probe tests a port

The health probe tests the first TCP port of the tunnel (or the one set with
`tunnel edit --probe-port`) every few seconds; `diag probe --all-ports` and
the report every 60 seconds test every port. Each TCP port map has a probe
kind (Advanced):

| Kind | A probe passes when … |
| --- | --- |
| `auto` (default) | a TLS hello gets any answer: TLS or not, as long as the service answers |
| `tcp` | the TCP connection opens |
| `tls` | a TLS handshake completes or a TLS alert comes back |
| `http` | `HEAD /` gets an HTTP status line back |

`auto` suits almost every VPN service. Choose `tcp` for a service that stays
silent or closes the connection when it gets a TLS hello (some proxies do),
`tls` or `http` to make sure that the right kind of service answers. UDP port
maps are always `auto` (they are checked through the transport's units).

Set it with `deyroute port add … --probe <kind>` or `deyroute port set <tunnel>
<port> --probe <kind>`; in the menu, in Advanced mode: `4) Ports` →
`Probe kind *` (and the probe question of `Add port to tunnel` and of the
Add-tunnel wizard). The values are checked like `ports[].probe` in
`config.yaml` (`DEY-C013`).

Next: [add a backup node](backup-node.md). If something fails:
[Troubleshooting](troubleshooting.md).
