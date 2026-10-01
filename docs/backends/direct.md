# Direct backend (`direct/native`, `direct/haproxy`)

| | |
| --- | --- |
| Pinned version | `builtin` (`internal/backend/backends.yaml`): `direct/native` is part of the `deyroute` binary; `direct/haproxy` uses the distribution's `haproxy` package (QUESTIONS.md C.21) |
| HAProxy reference | [HAProxy configuration manual](https://docs.haproxy.org/) of the installed 1.8+ version (keys used exist since 1.5) |
| Package | `internal/backend/direct` (renderer and the relay data plane `RunRelay`) |
| Direction | Forward: the hub dials the node |

## Transports

| Transport id | Carries | Stealth | Client IP | Ladder |
| --- | --- | --- | --- | --- |
| `direct/native` | tcp, udp | 1 | masked (node sees the node relay) | last rung of both default ladders; **never quarantined** (spec 9) |
| `direct/haproxy` | tcp | 1 | preserved with `advanced.proxy_protocol: true` (PROXY v1) | optional (phase 7), never in a default ladder |

Neither transport hides anything: they exist so the service keeps working
when every obfuscated rung is blocked.

## direct/native

A TCP/UDP relay inside `deyroute` (spec 7.8 and 12): one unit per side runs

```text
/usr/local/bin/deyroute relay --tunnel <tunnel> --config <config dir>/relay.json
```

with the shared hardened `deyroute-tun@` template (user `deyroute`, only
`CAP_NET_BIND_SERVICE`). `RunRelay(ctx, configPath, logger)` is the entry
point the CLI calls.

### Hub half (`relay.json`)

```json
{
  "version": 1,
  "tunnel": "main",
  "role": "hub",
  "token": "<tunnel token>",
  "node": "1.2.3.4:30001",
  "ports": [
    { "index": 0, "proto": "tcp", "listen": "0.0.0.0:443" },
    { "index": 1, "proto": "udp", "listen": "0.0.0.0:443" }
  ],
  "idle_timeout_s": 300,
  "dial_timeout_s": 10,
  "udp_idle_timeout_s": 60,
  "max_udp_sessions": 4096
}
```

It binds every user port (purpose `user`) on the listen address (`::` for
dual stack, loopback for the canary) and forwards to `<node_ip>:<ctl>`. The
hub half never learns the targets.

### Node half (`relay.json`)

```json
{
  "version": 1,
  "tunnel": "main",
  "role": "node",
  "token": "<tunnel token>",
  "bind": "0.0.0.0:30001",
  "ports": [
    { "index": 0, "proto": "tcp", "target": "127.0.0.1:443" },
    { "index": 1, "proto": "udp", "target": "127.0.0.1:443" }
  ],
  "idle_timeout_s": 300, "dial_timeout_s": 10,
  "udp_idle_timeout_s": 60, "max_udp_sessions": 4096
}
```

It listens on the backend control port (`tcp` when the tunnel has TCP port
maps, `udp` when it has UDP ones; `::` when the node's public IP is IPv6).
`ports` is the node's **allow-list**: the hub names a target only by its
index, and the node dials nothing but these targets (spec 11 "the node must
not become an open proxy", scenario S17). The file is parsed strictly
(unknown keys are `DEY-B060`).

### Wire protocol

- **TCP.** One connection to the node per user connection, starting with a
  32-byte preamble: `"DEYR"` · version `1` · flags `0` · index (uint16 BE) ·
  8-byte random nonce · first 16 bytes of `HMAC-SHA256(token, first 16 bytes)`.
  The node checks the MAC in constant time before anything else, then the
  version, the flags, that the nonce was not seen in the last 5 minutes
  (bounded cache of 65 536 nonces), and that the index is a TCP target. On
  any failure the connection is closed without a reply. Then both directions
  are copied with `ReadFrom`/splice(2) between the TCP sockets (32 KB pooled
  buffers only when splice is unavailable) and EOF is propagated as a
  half-close. A connection with no byte in either direction for 5 minutes is
  closed: each direction reports progress at least every 30 s (a read
  deadline, never a write deadline, which could drop bytes splice already
  holds) and a per-connection watchdog timer closes both sockets after
  5 min 30 s without progress; this also reaps connections whose peer stopped
  reading. Dials (hub → node, node → target) time out after 10 s; at most
  1 024 handshakes may be pending on the node.
- **UDP.** One datagram per user datagram, both directions:
  `"DEYU"` · index (uint16) · session id (uint64) · payload · 16-byte
  truncated `HMAC-SHA256(token, dir | header | payload)`, where `dir` is a
  direction byte that is not sent (`1` hub → node, `2` node → hub), so a
  frame captured in one direction cannot be reflected as valid in the other. The hub keeps one session
  per (port, client address) with a random id and a single connected socket to
  the node; the node keeps one connected socket to the target per session.
  Sessions expire after 60 s without traffic, at most 4 096 per relay; user
  datagrams larger than 16 KB are dropped. A session id is bound to the hub
  address that used it for 5 minutes, so a captured datagram replayed from
  another (spoofed) address is not relayed.

### Errors

| Code | When |
| --- | --- |
| `DEY-B060` | `relay.json` missing, not valid JSON, unknown key or invalid value |
| `DEY-B061` | a listen port or the node address cannot be opened (port in use, address not local) |
| `DEY-B006` / `DEY-B010` | `Validate`: no ports, invalid target, missing token / node IP / binary path, unsupported protocol |

## direct/haproxy

HAProxy on the hub forwards each listen port straight to
`<node_ip>:<target port>` in `mode tcp`; no process relays on the node.

### Hub: `haproxy.cfg`

```haproxy
global
    maxconn 65536

defaults
    mode tcp
    timeout connect 10s
    timeout client 5m
    timeout server 5m
    timeout client-fin 30s
    timeout server-fin 30s
    timeout check 5s

frontend tcp-443
    bind 0.0.0.0:443
    default_backend tcp-443

backend tcp-443
    option tcp-check
    server de-1 1.2.3.4:443 check inter 5s fall 3 rise 2 send-proxy check-send-proxy
```

`send-proxy check-send-proxy` is added only with
`advanced.proxy_protocol: true`; the node service must then accept the PROXY
protocol (e.g. Xray `acceptProxyProtocol`). Unit:
`ExecStartPre=<haproxy> -c -f <config dir>/haproxy.cfg` and
`ExecStart=<haproxy> -f <config dir>/haproxy.cfg -db` (foreground), where
`<haproxy>` is `Paths.Binary` (the distribution binary, e.g.
`/usr/sbin/haproxy`). An IPv6 listen address renders `bind :::443 v4v6`
(HAProxy takes the port after the last colon).

### Node: reachability check

Nothing needs to run on the node, but the failover engine starts the server
side of a Forward transport first, so the node side is a `oneshot` unit
(`RemainAfterExit=yes`) running `deyroute relay` with `"role": "check"`: for
each target it connects to `<node_ip>:<port>` and, for `0.0.0.0`/`::`
targets, to the node's other non-loopback addresses (1:1 NAT clouds), and
fails the start with `DEY-B062` when nothing answers. The check needs no
token. Listing the local addresses needs a netlink socket, so this unit (and
only this one) adds `AF_NETLINK` to `RestrictAddressFamilies`.

### Target rules

HAProxy connects from the hub, so the service must listen on an address the
hub can reach. `Validate` accepts only targets whose host is the node's
public IP or the unspecified address (`0.0.0.0:443`, `[::]:443` meaning "the
service listens on every interface") and returns `DEY-B006` otherwise — in
particular for the default target `127.0.0.1:<listen>` — with the exact
target to write instead. `Paths.Binary` empty (HAProxy not installed on the
hub) is `DEY-B006` too. `Validate` checks the hub-side input; the node side
needs only the `deyroute` binary.

## Differences from the spec (section 7.8) and why

| Spec | Implementation | Reason |
| --- | --- | --- |
| `direct/native`: "Hub → `node_ip:<target>`" | hub half → `<node_ip>:<ctl>` → node half → target | the default target is `127.0.0.1:<listen>` (QUESTIONS.md A.1): a service bound to loopback on the node is unreachable from the hub, and a relay on the node lets `direct/native` work for every target while keeping the node from being an open proxy (allow-list, spec 11). The control port is already opened only between hub and node |
| "`deyroute relay --tunnel <id>`" | `deyroute relay --tunnel <id> --config <file>` | one tunnel has one relay per (node, transport) and per side; the unit names the exact rendered file |
| relay "with io.Copy/splice and 32 KB buffers" | `ReadFrom` on `*net.TCPConn` (splice) in 256 KB chunks with 30 s read deadlines that report progress to an idle watchdog; 32 KB pooled buffers for the non-splice path | same kernel zero-copy path as `io.Copy`, plus the 5-minute idle timeout without losing spliced bytes |
| `direct/haproxy`: "Hub → `node_ip:<target>`" with `send-proxy` | as specified; targets must name a reachable address; node side is a check-only oneshot | see "Target rules" |
| `send-proxy` "when the node service has `acceptProxyProtocol`" | `advanced.proxy_protocol: true` (owner decides; deyroute cannot see the service config) | QUESTIONS.md C.14 / A.3 |

## Known limitations

- No obfuscation, no encryption of user bytes by the relay (the user's own
  TLS passes through untouched). The HMAC only authenticates the hub.
- A captured TCP preamble can be replayed after the 5-minute window (or
  after 65 536 newer connections). It only opens a connection to the same
  target the public hub port already reaches. The UDP session binding has the
  same 5-minute horizon.
- The node relay listens on `0.0.0.0:<ctl>` and trusts only the token, not
  the source address (a hub behind NAT or with several addresses must keep
  working). Anyone who can reach that port can hold up to 1 024 pending
  handshakes for 10 s each and delay new relayed connections; restricting
  the control port range to the hub's IP in the node firewall removes this.
- UDP replies leave the hub from the listen socket; on a hub with several
  public IPs a client that sent to a secondary IP may drop replies that the
  kernel sources from the primary IP.
- `direct/haproxy` needs the node service reachable on the node's public
  address (listening on `0.0.0.0` and allowed from the hub IP), carries TCP
  only, and exposes the service port on the node to the hub (and, unless the
  node firewall restricts it, to everyone).
- HAProxy traffic logs are off (no `log` directive): no client IPs are
  written to disk; start-up errors still reach the tunnel log via stderr.
