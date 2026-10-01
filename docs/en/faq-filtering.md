# Filtering FAQ

[فارسی](../fa/faq-filtering.md) · [Index](index.md)

## How does DEYROUTE deal with filtering?

Single-method tunnels die with one change in the filtering. DEYROUTE gives
every tunnel a **ladder**: an ordered list of different tunnel methods
(transports) from different programs. Only one rung carries traffic at a
time; the others are warm. When the active rung is blocked, the hub notices
it within a few probes and starts the next rung — normally within
35 seconds, without you doing anything. When rung 1 works again, the tunnel
returns to it. Details and timings: [Backup node](backup-node.md).

## The default ladder

| # | Transport | Why it is on the ladder | Backend page |
| --- | --- | --- | --- |
| 1 | `backhaul/wssmux` | TLS + WebSocket + multiplexing: looks like HTTPS, few connections, fast | [backhaul](../backends/backhaul.md) |
| 2 | `backhaul/tcpmux` | no TLS but multiplexed; the least overhead, for when the tunnel's TLS fingerprint is the problem | [backhaul](../backends/backhaul.md) |
| 3 | `rathole/noise` | a different program (Rust) and protocol with Noise encryption: a cheap alternative if the Backhaul pattern is recognised | [rathole](../backends/rathole.md) |
| 4 | `frp/tcp` | a third program with a different traffic pattern (TLS on the wire) | [frp](../backends/frp.md) |
| 5 | `xray/reality` | looks like a real TLS 1.3 connection to a decoy website; the most hidden option without UDP | [xray](../backends/xray.md) |
| 6 | `hysteria2/udp` | QUIC; only when UDP is open; best throughput on lossy links | [hysteria2](../backends/hysteria2.md) |
| 7 | `waterwall/reverse-reality` | reverse connection with a Reality look; the last hidden line of defence | [waterwall](../backends/waterwall.md) |
| 8 | `direct/native` | no disguise at all; only so the service does not go down | [direct](../backends/direct.md) |

Tunnels with only UDP ports use the `udp-default` ladder:
`backhaul/udp` → `hysteria2/udp` → `wireguard/kernel` → `direct/native`.
Rungs that cannot carry a tunnel's protocol are left out automatically.

Optional transports, never in a default ladder: `direct/haproxy`,
`awg/userspace` ([wireguard](../backends/wireguard.md)),
`gost/relay-wss` ([gost](../backends/gost.md)),
`chisel/wss` ([chisel](../backends/chisel.md)), and the other methods of
each backend (`backhaul/ws`, `rathole/tls`, `frp/quic` …; see the backend
pages).

In Simple mode you never see the ladder; the dashboard shows which rung each
tunnel uses. In Advanced mode `5) Failover` → `2) Ladder order` edits it.

## Can I change the order?

Yes. The built-in ladders `default` and `udp-default` cannot be edited
(`DEY-C022`), but you can create your own and give it to a tunnel:

```bash
deyroute ladder list
deyroute ladder show default
deyroute ladder create stealth --rungs xray/reality,waterwall/reverse-reality,backhaul/wssmux,direct/native
deyroute tunnel edit main --ladder stealth
deyroute ladder set stealth --rungs xray/reality,backhaul/wssmux,direct/native
```

A simple guide:

| If … | Suggestion |
| --- | --- |
| speed matters more than hiding | put `tcpmux` first |
| filtering recognises the tunnel TLS | move `xray/reality` up |
| UDP is open in the Iran datacenter | put `hysteria2/udp` second |
| the node only has TCP services | remove the udp rungs |

To try every rung once and compare RTTs:
`deyroute tunnel test-ladder main` (20 seconds per rung; the tunnel is
interrupted during the test).

## What does "skipped rung" mean?

A rung that cannot be used for **this** tunnel right now. It is taken out of
the tunnel's ladder with a yellow warning (event `rung_skipped`), shown as
`skipped: <reason>` in `deyroute tunnel show main`, and tested again every
30 minutes; when it passes it comes back (`rung_restored`). It is a warning,
not an error. Typical reasons:

- **UDP is blocked** between hub and node, or not tested yet (`DEY-B007`):
  `hysteria2/udp`, `wireguard/kernel`, `backhaul/udp`, `frp/quic`,
  `frp/kcp` need UDP and are used only after the UDP test passed. The test
  runs when the node connects and on every 30-minute re-check.
- **The transport cannot be used** (`DEY-B006`) — for example `frp/wss` is
  not available with the pinned FRP release (see
  [frp](../backends/frp.md)), or `direct/haproxy` is chosen but the hub has no
  `haproxy` program.
- **The transport cannot carry the tunnel's protocol** (`DEY-B010`), e.g. a
  TCP-only rung in a UDP tunnel; such rungs are simply left out.

## Decoy SNI

`xray/reality` and `waterwall/reverse-reality` make the tunnel look like a
TLS 1.3 connection to a real, harmless website (the decoy). Before such a
rung is used, the hub checks which decoy answers and takes the first
reachable one. If none answers, you get `DEY-B042`.

The built-in list is only a placeholder. Set three TLS 1.3 sites that are
reachable from your hub's datacenter and not blocked in Iran, under `hub:` in
`config.yaml`:

```bash
deyroute config edit
```

```yaml
hub:
  decoy_snis: [www.example-one.com, www.example-two.com, www.example-three.com]
```

Saving validates and applies the file.

## Why doesn't DEYROUTE touch my TLS?

Your service (ws+tls, tcp+tls, Reality …) runs on the node with its own
certificate, SNI and path. The tunnel only carries the raw bytes of each
TCP/UDP connection from the hub port to the node; it never decrypts or
re-encrypts them. Therefore:

- no TLS errors come from the tunnel, and nothing about your service changes;
- in client configs only the address changes (to the hub IP); SNI, host,
  path, UUID and fingerprint stay the same;
- the hub cannot see what your users send.

Some transports (e.g. `backhaul/wssmux`, `frp/*`) wrap the tunnel itself in
TLS; that is a separate certificate DEYROUTE creates for the tunnel (`auto`
mode, pinned to its internal CA), independent of your service's certificate.
See [Security](security.md).

## Will my panel see the users' real IP?

Usually not. For almost every transport the service on the node sees a local
or hub-side address instead of the user's IP; `deyroute tunnel show main`
prints `client IP masked`. Only `direct/haproxy` can pass the real IP, with
PROXY protocol, when `advanced.proxy_protocol: true` is set for the tunnel and
your service accepts PROXY protocol; it is then shown as
`client IP preserved`. `direct/haproxy` has no disguise, so it is not a
filtering-resistant option.

## What DEYROUTE does not do

- **It cannot measure filtering inside Iran.** `deyroute port check` tests the
  port from a node, i.e. from the internet. Whether users in Iran can reach
  `HUB_IP:PORT` is not measured; `Note: … it does not measure filtering inside Iran.`
- **It cannot unblock the hub.** If the hub's IP or port is filtered for your
  users, every transport is affected. Use another port or move the hub to a
  new IP ([Hub move](hub-move.md)).
- **It cannot tell why a rung failed** beyond what the probe sees (timeout,
  reset, no answer). It only moves on to a rung that works.
- **It does not guarantee that any transport stays open.** The ladder buys
  time and choice; it does not defeat every kind of filtering.
- **It is not a VPN panel.** It does not create users or client configs and
  does not copy your service to backup nodes.
- **It sends no data anywhere** (no telemetry). The only outside traffic is
  downloading releases and backend binaries from GitHub or your mirror, and
  Telegram messages if you turn them on.

## The node's IP is blocked — what now?

When every rung to one node times out, the node's IP is probably blocked from
the hub (`deyroute doctor` says so). With a backup node the tunnel has already
moved there. Otherwise give the node a new IP — it reconnects to the hub by
itself — or join another server and add it as a backup.
