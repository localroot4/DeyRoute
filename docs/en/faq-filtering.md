# Filtering FAQ

[فارسی](../fa/faq-filtering.md) · [Index](index.md)

## How does DEYROUTE deal with filtering?

Single-method tunnels die with one change in the filtering. DEYROUTE gives
every tunnel a **ladder**: an ordered list of different tunnel methods
(transports) from different programs. Only one rung carries traffic at a
time; the others are warm. When the active rung is blocked, the hub notices
it within a few probes and starts the next rung — normally within
35 seconds, without you doing anything. That time was measured with a block
that stays in place (lab S08). A filter that lets each connection run for a
few seconds and then cuts it may be noticed late, or not at all. When rung 1
works again, the tunnel returns to it. Details and timings:
[Backup node](backup-node.md).

## The default ladder

| # | Transport | Why it is on the ladder | Backend page |
| --- | --- | --- | --- |
| 1 | `backhaul/wssmux` | TLS + WebSocket + multiplexing: looks like HTTPS, few connections, fast | [backhaul](../backends/backhaul.md) |
| 2 | `backhaul/tcpmux` | no TLS but multiplexed; the least overhead, for when the tunnel's TLS fingerprint is the problem | [backhaul](../backends/backhaul.md) |
| 3 | `rathole/noise` | a different program (Rust) and protocol with Noise encryption: a cheap alternative if the Backhaul pattern is recognised | [rathole](../backends/rathole.md) |
| 4 | `frp/tcp` | a third program with a different traffic pattern (TLS on the wire) | [frp](../backends/frp.md) |
| 5 | `xray/reality` | looks like a real TLS 1.3 connection to a decoy website; the most hidden option without UDP | [xray](../backends/xray.md) |
| 6 | `hysteria2/udp` | QUIC hidden by Salamander obfuscation, on a UDP port in 30000–31999 (20000–20999 with port hopping), never 443; needs UDP; best throughput on lossy links | [hysteria2](../backends/hysteria2.md) |
| 7 | `waterwall/reverse-reality` | reverse connection with a Reality look; the last hidden line of defence | [waterwall](../backends/waterwall.md) |
| 8 | `direct/native` | no disguise at all; only so the service does not go down | [direct](../backends/direct.md) |

Tunnels with only UDP ports use the `udp-default` ladder:
`backhaul/udp` → `hysteria2/udp` → `direct/native`.
Rungs that cannot carry a tunnel's protocol are left out automatically.
`wireguard/kernel` was rung 3 of `udp-default` until October 2026. It left
after the [field report](#field-report-iran-october-2026) found plain
WireGuard blocked. To use it on a tunnel anyway, give the tunnel an inline
ladder:

```bash
deyroute tunnel edit games --ladder backhaul/udp,hysteria2/udp,wireguard/kernel,direct/native
```

Optional transports, never in a default ladder: `direct/haproxy`,
`wireguard/kernel` and `awg/userspace` ([wireguard](../backends/wireguard.md)),
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
| UDP passes between hub and node (UDP `ok` in `deyroute node test`) | put `hysteria2/udp` second, then check it with `test-ladder`: the UDP test only shows that plain datagrams pass |
| the node only has TCP services | remove the udp rungs |
| the node's own UDP service is WireGuard, OpenVPN or QUIC | `backhaul/udp` passes its datagrams on as they are and `direct/native` only puts a short header in front of them, so a filter may still recognise them (not tested). Of the `udp-default` rungs only `hysteria2/udp` encrypts them again. Rungs that carry UDP port maps inside one encrypted TCP connection, such as `xray/reality` (XUDP) and `rathole/noise`, hide them too, but UDP over TCP adds delay when packets are lost: `deyroute ladder create udp-stealth --rungs hysteria2/udp,xray/reality,backhaul/udp,direct/native` |
| on a hub with no domain, rungs 1 and 4 connect and are then cut | `deyroute ladder create no-sni --rungs rathole/noise,xray/reality,waterwall/reverse-reality,backhaul/tcpmux,direct/native` (not the `stealth` example above: it has `backhaul/wssmux`) |

Without `hub.domain`, the tunnel TLS of `backhaul/wssmux` and `frp/tcp`
carries no server name, since they dial the hub's IP. Setting `hub.domain`
gives `frp/tcp`, `gost`, `chisel` and `rathole/tls` a real server name, but
not `backhaul/wss` and `backhaul/wssmux`: they always dial the hub's IP.

To try every rung and compare RTTs: `deyroute tunnel test-ladder main`. Each
rung gets 20 seconds. A rung that passes its first probe keeps running and
is probed every 5 seconds (`probe_interval_s`) until its 20 seconds are
over; its RTT is the median of those probes. A rung that passes and then
fails is reported with `DEY-B073`. The tunnel is interrupted during the
whole test. For a tunnel with only UDP ports the test checks only that the
units of each rung run (see below), and says so.

## What does "skipped rung" mean?

A rung that cannot be used for **this** tunnel right now. It is taken out of
the tunnel's ladder with a yellow warning (event `rung_skipped`), shown as
`skipped: <reason>` in `deyroute tunnel show main`, and tested again every
30 minutes; when it passes it comes back (`rung_restored`). It is a warning,
not an error. Typical reasons:

- **UDP is blocked** between hub and node, or not tested yet (`DEY-B007`):
  `hysteria2/udp`, `wireguard/kernel`, `backhaul/udp`, `frp/quic`,
  `frp/kcp` need UDP and are used only after the UDP test passed. The test
  runs when the node connects and on every 30-minute re-check. It sends up
  to three small plain datagrams from the hub to a UDP port of the node
  (30000–31999) and waits for their echo. A pass only shows that plain
  datagrams get through; a filter that recognises WireGuard, OpenVPN or
  QUIC by their signature can still cut those rungs.
- **The transport cannot be used** (`DEY-B006`) — for example `frp/wss` is
  not available with the pinned FRP release (see
  [frp](../backends/frp.md)), or `direct/haproxy` is chosen but the hub has no
  `haproxy` program.
- **The transport cannot carry the tunnel's protocol** (`DEY-B010`), e.g. a
  TCP-only rung in a UDP tunnel; such rungs are simply left out.

## How is a tunnel with only UDP ports checked?

The hub probes a tunnel by connecting to one of its TCP ports through the
tunnel. A tunnel with only UDP ports has none, so its rungs are checked only
by their units: the active rung counts as working while its unit runs on
the hub and on the node. If a filter cuts its traffic while the units keep
running, the tunnel stays UP and does not switch. Watch such a tunnel
through your service (its logs, your users) and switch by hand when needed:
`deyroute tunnel switch games --transport hysteria2/udp`. `test-ladder` has
the same limit and says so.

## Port hopping and the node's other UDP services

With `advanced.hysteria_port_hopping: true` on a tunnel, the hub sends the
datagrams of `hysteria2/udp` to changing ports in 20000–20999 of the node,
and the node redirects that range to the rung's own port. The redirect
matches only datagrams from the hub's public addresses (`hub.public_ip` and
`hub.public_ip6`) to the node's public address. A UDP service of your own
on the node in 20000–20999 keeps the traffic of every other source. Check
the rule on the node:

```bash
nft list chain inet deyroute prerouting
```

```text
fib daddr type local ip saddr 5.6.7.8 ip daddr 1.2.3.4 udp dport 20000-20999 redirect to :30001
```

- **Node behind 1:1 NAT.** Many cloud providers map the public IP to a
  private address on the server (`ip -br addr` on the node does not show
  the public IP). Packets then arrive with the private address as
  destination. The node notices this, leaves out `ip daddr` and matches
  the hub's addresses only. It logs this once:
  `NAT destination … is not an address of this node`.
- **The hub's address changed** (a [hub move](hub-move.md), a new
  `hub.public_ip`): the rule follows on the next `deyroute config apply`
  and whenever the node connects to the hub.
- **The hub must send from its public address.** Datagrams that leave the
  hub from another address do not match. The rung then fails its probes
  and the tunnel moves to the next rung. Turn port hopping off for that
  tunnel.
- **Not scoped:** a node with an IPv6 `public_ip` while the hub has no
  `hub.public_ip6`, and a node with an older DEYROUTE version (until you
  update it). These keep the old rule, which redirects the whole range
  from every source. Keep other UDP services out of 20000–20999 there.

## Decoy SNI

`xray/reality` and `waterwall/reverse-reality` make the tunnel look like a
TLS 1.3 connection to a real, harmless website (the decoy). Before such a
rung is used, the hub checks which decoy answers with TLS 1.3 and HTTP/2
and takes the first one that does. If none answers, you get `DEY-B042`.

The check runs on the hub over the address family of `hub.public_ip` (IPv4
when it is IPv4), so a decoy must answer from the hub over that family. The
hub looks each name up first and connects only to the public addresses it
gets. When its resolver answers only with private or reserved addresses, the
decoy fails without a connection, and the reason in the `DEY-B042` event
says `resolves to a private address from the hub (…; DNS tampering?)`. The
hub's DNS is then likely tampered with: choose other decoys, or point the
hub at a resolver that is not (`/etc/resolv.conf`; check with
`dig +short NAME`).

The built-in list is `dl.google.com`, `www.speedtest.net` and
`www.samsung.com`; each one is tested with both rungs. You only need your
own list when none of them works from your hub's datacenter. Then set TLS 1.3
sites with HTTP/2 that are reachable there and not blocked in Iran, under
`hub:` in `config.yaml` (avoid `www.microsoft.com`: `xray/reality` failed the ladder
test with it):

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

## Field report: Iran, October 2026

A filtering report of 2026-10-17 measured connections from one datacenter
in Iran (Parvaz) to Hetzner in Germany and to Google and Cloudflare. Every
connection was started from Iran, and only standard VPN protocols were
tested. What it means for DEYROUTE:

- **Measured direction.** In the Forward rungs the hub, in Iran, dials the
  node: `xray/reality`, `hysteria2/udp`, `wireguard/kernel`,
  `awg/userspace`, `direct/native` and `direct/haproxy`. They go in the
  direction the report tested. In the Reverse rungs the node dials the
  hub: every `backhaul`, `rathole`, `frp`, `waterwall`, `gost` and `chisel`
  rung. That direction was **not** measured.
- **WireGuard.** Plain WireGuard was blocked over IPv4 (cut after about
  2 seconds) and over IPv6 (no answer). DEYROUTE's UDP test still passes on
  such a network, and a tunnel with only UDP ports does not notice that its
  traffic is cut (see above). This is why `wireguard/kernel` left
  `udp-default`.
- **Not tested.** Salamander (`hysteria2/udp`), Reality (`xray/reality`,
  `waterwall/reverse-reality`) and Noise (`rathole/noise`) were not part of
  the report. It says nothing about them.
- **IPv6.** Do not move nodes to the hub's IPv6 address because of the
  report. IPv6 between hub and node is not supported, and the hub firewall
  then drops the rungs in which the node dials the hub; see
  [Join](join.md#do-not-connect-a-node-over-ipv6).

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
