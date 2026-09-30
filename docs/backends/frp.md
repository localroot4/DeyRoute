# FRP backend (`frp/*`)

| | |
| --- | --- |
| Upstream | [fatedier/frp](https://github.com/fatedier/frp) |
| Pinned version | **v0.71.0** (`internal/backend/backends.yaml`; `pkg/util/version/version.go` at the tag says `0.71.0`) |
| Reference | [README at v0.71.0](https://github.com/fatedier/frp/blob/v0.71.0/README.md), `conf/frps_full_example.toml`, `conf/frpc_full_example.toml` and the v0.71.0 source (`pkg/config/v1/{server,client,common,proxy}.go`, `pkg/config/load.go`, `client/connector.go`, `server/service.go`, `pkg/util/net/{tls,websocket,dial}.go`, `test/e2e/v1/basic/client_server.go`) |
| Release assets | `frp_0.71.0_linux_amd64.tar.gz`, `frp_0.71.0_linux_arm64.tar.gz` (`package.sh`), binaries `frps` and `frpc` inside `frp_0.71.0_linux_<arch>/`, upstream checksums in `frp_sha256_checksums.txt` |
| Package | `internal/backend/frp` |
| Direction | Reverse for every transport: the hub runs `frps`, the node runs `frpc` |

## Transports

| Transport id | Carries | Needs UDP between hub and node | Needs tunnel TLS | Stealth | On the wire |
| --- | --- | --- | --- | --- | --- |
| `frp/wss` | — | no | yes | 4 | **not available with v0.71.0** (see below) — `Validate` returns `DEY-B006` |
| `frp/websocket` | tcp, udp | no | yes | 2 | plaintext HTTP upgrade to `/~!frp`, then TLS inside the WebSocket |
| `frp/quic` | tcp, udp | **yes** | yes | 3 | QUIC (TLS 1.3, ALPN `frp`) on `<ctl>/udp` |
| `frp/kcp` | tcp, udp | **yes** | yes | 2 | KCP over UDP on `<ctl>/udp`, TLS inside the KCP stream |
| `frp/tcp` | tcp, udp | no | yes | 3 | one standard TLS session (no frp `0x17` marker byte) with yamux inside |

TLS is mandatory for every frp transport (spec 7.3): frps has
`transport.tls.force = true` and the tunnel certificate, frpc verifies it
against the internal CA. Stealth follows the section 7 table where it gives a
value (`wss:4`, `quic:3`); the others reflect what an observer sees: `tcp`
looks like any TLS session (3), `websocket` exposes frp's fixed path
`/~!frp` in plaintext before TLS (2), `kcp` has plaintext KCP headers (2).

`frp/quic` and `frp/kcp` run over UDP between node and hub, so they set
`NeedsUDP` (the spec only names Hysteria2/WireGuard; the same UDP
reachability check and skip logic applies — QUESTIONS.md). UDP port maps are
frp `type = "udp"` proxies and work on every transport.

### Why `frp/wss` is refused

frpc v0.71.0 speaks `wss` as TLS first and the WebSocket upgrade *inside* TLS
(`client/connector.go`: TLS hook priority 100, WebSocket hook 110). frps only
recognises a WebSocket upgrade as plaintext `GET /~!frp` on `bindPort`
(`server/service.go` muxer); a TLS connection (`0x16`) goes to the TLS
listener, which serves yamux directly on the decrypted stream and cannot
parse the upgrade. Upstream says so in its e2e test: *"frps only supports
ws, so there should be a proxy to terminate TLS before frps"*. Running
frpc `wss` against the rendered frps confirms it (`connect to server error:
unexpected EOF`). A deyroute unit runs exactly one process, so there is no TLS
terminator; in addition `wss` cannot be combined with `transport.tls.force`
because the stream reaching frps behind a terminator is no longer TLS.

`frp/wss` therefore stays registered (it is rung 4 of the default ladder and a
known transport id) but `Validate`/`Render` return `DEY-B006` with the reason,
so the planner skips it with the usual yellow `rung_skipped` warning. `frp/tcp`
has the same outside appearance (a TLS session to the control port) and is the
recommended replacement (QUESTIONS.md).

## How each side is rendered

Files are written to `/etc/deyroute/backends/frp/<tunnel>/<node>/<transport>/`
(`0750` directory, `0640` files, `root:deyroute`); the unit is the
`deyroute-tun@<tunnel>.<node>.frp-<transport>` instance of the shared hardened
template.

### Hub: `frps.toml`

```toml
bindAddr = "0.0.0.0"
bindPort = 30001                         # the per-(tunnel, node, transport) control port (tcp)
quicBindPort = 30001                     # frp/quic only (udp)
# kcpBindPort = 30001                    # frp/kcp only (udp)
proxyBindAddr = "0.0.0.0"                # listen address of the user ports ("::", "127.0.0.1" canary)
allowPorts = [
  { single = 443 },                      # exactly the tunnel's listen ports
  { single = 2053 },
]
log.to = "console"                       # stdout → /var/log/deyroute/tunnels/<tunnel>.log
log.level = "info"
log.disablePrintColor = true
auth.method = "token"
auth.token = "<tunnel token>"
transport.maxPoolCount = 32
transport.tcpMux = true
transport.tls.force = true
transport.tls.certFile = "<config dir>/tls-cert.pem"
transport.tls.keyFile = "<config dir>/tls-key.pem"
```

Unit: `ExecStart=<bin dir>/frps -c <config dir>/frps.toml`,
`WorkingDirectory=<config dir>`. Binds: `tcp 0.0.0.0:<ctl>` (control; frps
always listens on `bindPort`), `udp 0.0.0.0:<ctl>` for quic/kcp (control), and
every listen port with its protocol on `proxyBindAddr` (user).

### Node: `frpc.toml`

```toml
serverAddr = "5.6.7.8"                   # hub public IP (bare, also for IPv6)
serverPort = 30001
loginFailExit = false                    # keep retrying while the hub unit restarts
log.to = "console"
log.level = "info"
log.disablePrintColor = true
auth.method = "token"
auth.token = "<tunnel token>"
transport.protocol = "tcp"               # websocket | quic | kcp | tcp
transport.poolCount = 8                  # advanced.connection_pool, capped at 32
transport.tcpMux = true
transport.tls.enable = true
transport.tls.disableCustomTLSFirstByte = true
transport.tls.trustedCaFile = "<config dir>/ca.crt"   # tls.mode acme/custom: "<config dir>/tls-cert.pem"
transport.tls.serverName = "<server name>"   # domain or hub IP; omitted when unknown

[[proxies]]                              # one per port map
name = "tcp-443"
type = "tcp"                             # "udp" for UDP port maps
localIP = "127.0.0.1"                    # target host
localPort = 443                          # target port
remotePort = 443                         # listen port on the hub
```

Unit: `ExecStart=<bin dir>/frpc -c <config dir>/frpc.toml`. The node binds
nothing (frpc's admin `webServer` stays disabled).

### Rendering rules

- Proxy names are `<proto>-<listen>`; a `(proto, listen)` pair listed twice
  is `DEY-B006`. An empty target defaults to `127.0.0.1:<listen>`, an empty
  proto to `tcp`. IPv6 targets (`[::1]:443`) become `localIP = "::1"`.
- `allowPorts` lists each listen port once (`{ single = N }`), in port-map
  order; frps applies it to both its TCP and UDP port managers.
- Canary: only the first port map, `proxyBindAddr` forced to loopback.
- `transport.poolCount` = `advanced.connection_pool` or 8, capped at the
  server's `maxPoolCount` 32; a negative value is `DEY-B006` (v0.71.0 rejects
  negative pool counts).
- frp renders every config file through Go `text/template` before parsing it
  (`pkg/config/load.go`), so `Validate` rejects `{{`/`}}` in any rendered value
  (token, TLS paths, server name, hub IP, listen address, targets).
- `Validate` also returns `DEY-B006` for a missing token, hub IP, TLS
  certificate/key, CA, binary directory or config directory; a proto other
  than tcp/udp is `DEY-B010`.
- frp needs no generated keys (no `KeyGenerator`).
- Trust anchor (`transport.tls.trustedCaFile`) by `tunnels[].tls.mode`. frpc
  builds its root pool from that one file and never uses the system roots
  (`pkg/transport/tls.go`), so:

  | `tls.mode` | `trustedCaFile` | Why |
  | --- | --- | --- |
  | `auto` (or empty) | `ca.crt` (internal CA) | spec 7.3: frpc is pinned to our CA |
  | `acme`, `custom` | `tls-cert.pem` (the served chain; public, copied to the node by the planner) | the internal CA did not issue this certificate and would reject it. Go's verifier accepts a leaf that is itself in the root pool, so this pins exactly the certificate the hub presents (host name still verified), and keeps working if ACME falls back to an internal certificate. After a renewal the planner re-renders both sides |

  Checked with the real v0.71.0 binaries: a hub certificate from a foreign
  CA fails the login with `trustedCaFile = ca.crt` (`session shutdown`) and
  succeeds with the served chain file; `TestLeafPinningVerifies` covers the
  Go behaviour in unit tests.
- Ids in the header comment are sanitised (no line breaks, no `{`/`}`),
  because frp executes comments as template text too.

## Differences between the spec sample (section 7.3) and v0.71.0

| Spec sample | Real v0.71.0 | What deyroute renders |
| --- | --- | --- |
| `transport.protocol = "wss"` with `transport.tls.force = true` | frps cannot accept `wss` without a separate TLS terminator; behind one the stream is no longer TLS, so `force` would reject it | `frp/wss` refused with `DEY-B006` (see above) |
| `allowPorts = [{ start = 443, end = 443 }, …]` | `start`/`end` and `single` both exist (`types.PortsRange`) | `{ single = 443 }` |
| `transport.tls.certFile = "/etc/deyroute/secrets/tls/main/cert.pem"` | any readable path | the copies in the unit's config dir (`tls-cert.pem`, `tls-key.pem`, `ca.crt`, QUESTIONS.md C.16), because the backend runs as `deyroute` and cannot read `secrets/` |
| `quicBindPort = 30003` always | `quicBindPort`/`kcpBindPort` open UDP listeners (0 = disabled) | only for `frp/quic` / `frp/kcp` respectively |
| no log keys | `log.to` defaults to `console`, colors on | `log.to = "console"`, `log.disablePrintColor = true` |
| no `proxyBindAddr` | defaults to `bindAddr` | always rendered (listen address, canary loopback) |
| no `transport.tls.serverName` | frpc falls back to `serverAddr` | `ServerName` (domain or hub IP) when known |
| no `transport.tls.disableCustomTLSFirstByte` | default `true` since v0.50.0 | rendered `true` explicitly (standard ClientHello) |
| no `bindAddr` | default `0.0.0.0` | rendered explicitly |
| `frps -c` / `frpc -c` | `--strict_config` defaults to true: an unknown key stops the binary | `frps -c frps.toml`, `frpc -c frpc.toml` |

## Verification done for this renderer

- Review pass: every golden (including `tcp_acme`) passes `frps verify -c`
  / `frpc verify -c` of v0.71.0 (strict: an unknown key is rejected).
- Every golden `frps.toml`/`frpc.toml` was loaded with frp v0.71.0's own
  `config.LoadServerConfig` / `config.LoadClientConfig` in strict mode and
  checked with its `validation` package (no errors, no warnings).
- frps and frpc v0.71.0 were built from the module source (only
  Go-1.24-compatibility patches to dependencies) and run on loopback with the
  golden `tcp_mixed`, `websocket`, `quic_mixed` and `kcp` configs, a
  deyroute-style Ed25519 CA and ECDSA P-256 tunnel certificate with an IP SAN:
  TCP and UDP echo passed through every transport; frpc `wss` against the
  same frps failed as described above.

## Security notes

- Reverse backend: frpc only dials the fixed `localIP:localPort` of each
  proxy, has no admin API, no visitors and no plugins, so the node cannot
  become an open proxy (spec 11). On the hub, `allowPorts` restricts the ports
  a client holding the token may bind to the tunnel's own listen ports, and
  frps has no dashboard, vhost, tcpmux or SSH-gateway listener.
- The control connection is always TLS, verified against the internal CA
  (`tls.mode auto`) or the served certificate chain (`acme`/`custom`) with
  the hub IP/domain as server name; the system roots are never trusted.
- `allowPorts` is not per protocol: a client holding the token could open
  UDP on a port the tunnel only maps for TCP (or vice versa). Only the
  tunnel's own nodes hold the token, and the port is one of the tunnel's
  listen ports anyway.
- Files are `0640 root:deyroute`; the token appears in both files, the TLS key
  only in the hub's copy.

## Known limitations

- `frp/wss` is unavailable (above). Replacing it in the default ladder by
  `frp/tcp`, or adding a TLS front, is an owner decision (QUESTIONS.md).
- `frp/quic` and `frp/kcp` need UDP between node and hub on the control port;
  when the UDP probe fails the rung is skipped (`DEY-B007`).
- The sha256 of both assets is still empty in the manifest (QUESTIONS.md C.9);
  installation is refused with `DEY-S006` until the release pipeline fills it.
- The TLS version is not configurable in frp v0.71.0 (no `minVersion` key);
  frps/frpc are Go programs and negotiate TLS 1.3 with each other (spec 10:
  TLS 1.3 only), but frps would also accept a TLS 1.2 client.
