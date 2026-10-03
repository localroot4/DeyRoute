# Backhaul backend (`backhaul/*`)

| | |
| --- | --- |
| Upstream | [Musixal/Backhaul](https://github.com/Musixal/Backhaul) |
| Pinned version | **v0.7.2** (`internal/backend/backends.yaml`; latest tag on the Go module proxy when pinned) |
| Reference | [README at v0.7.2](https://github.com/Musixal/Backhaul/blob/v0.7.2/README.md) and the v0.7.2 source (`config/config.go`, `cmd/defaults.go`, `internal/server/transport/*.go`) |
| Release assets | `backhaul_linux_amd64.tar.gz`, `backhaul_linux_arm64.tar.gz` (GoReleaser `name_template: {{ .ProjectName }}_{{ .Os }}_{{ .Arch }}`), binary `backhaul`, upstream checksums in `checksums.txt` |
| Package | `internal/backend/backhaul` |
| Direction | Reverse for every transport: the hub runs `[server]`, the node runs `[client]` |

## Transports

| Transport id | Carries | Needs UDP between hub and node | Needs tunnel TLS | Stealth | Notes |
| --- | --- | --- | --- | --- | --- |
| `backhaul/tcp` | tcp, udp | no | no | 1 | UDP port maps travel over the TCP tunnel (`accept_udp = true`) |
| `backhaul/tcpmux` | tcp | no | no | 2 | SMUX over TCP |
| `backhaul/ws` | tcp | no | no | 2 | WebSocket |
| `backhaul/wsmux` | tcp | no | no | 3 | SMUX over WebSocket |
| `backhaul/wss` | tcp | no | yes | 4 | WebSocket over TLS |
| `backhaul/wssmux` | tcp | no | yes | 4 | SMUX over WebSocket over TLS (rung 1 of the default ladder) |
| `backhaul/udp` | udp | **yes** | no | 1 | control channel over TCP, data over UDP, both on the control port (rung 1 of the UDP-only ladder) |

A transport that cannot carry a protocol of the tunnel fails `Validate` with
`DEY-B010` (for example `backhaul/udp` in a tunnel with a TCP port map); the
ladder resolver drops such rungs earlier.

### Tunnels with TCP and UDP port maps (UDP companion)

`backhaul/tcpmux`, `ws`, `wss`, `wsmux` and `wssmux` carry TCP only. In a
tunnel that has both TCP and UDP port maps they stay in the ladder: the hub
allocates a second control port for the rung (key
`<tunnel>/<node>/<transport>/udp`) and the same unit runs two Backhaul
processes:

- the main process (`server.toml` / `client.toml`) with the TCP maps on the
  rung's transport;
- the UDP companion (`server-udp.toml` / `client-udp.toml`) with the UDP
  maps on `backhaul/udp` and the second control port.

`ExecStart=/usr/local/bin/deyroute pair <backhaul> -c <dir>/server.toml -- <backhaul> -c <dir>/server-udp.toml`.
`deyroute pair` starts both, and when either exits it stops the other and
exits non-zero, so systemd restarts the pair and the failover sees one
candidate. A node accepts a `pair` unit only when every command line runs a
Backhaul binary under `/var/lib/deyroute/bin/backhaul/`. Because the companion
carries UDP datagrams, such a rung needs a passed UDP probe between hub and
node (section 7.6; `DEY-B007` until then). The canary unit of these rungs
runs the main process only (its one loopback port map is TCP).

## How each side is rendered

Files are written to `/etc/deyroute/backends/backhaul/<tunnel>/<node>/<transport>/`
(`0750` directory, `0640` files, `root:deyroute`); the unit is the
`deyroute-tun@<tunnel>.<node>.backhaul-<transport>` instance of the shared
hardened template.

### Hub: `server.toml`

```toml
[server]
bind_addr = "0.0.0.0:30001"          # the per-(tunnel, node, transport) control port
transport = "wssmux"
token = "<tunnel token>"
keepalive_period = 75
nodelay = true
heartbeat = 20
channel_size = 2048
mux_con = 8                          # mux_* only for tcpmux / wsmux / wssmux
mux_version = 1
mux_framesize = 32768
mux_recievebuffer = 4194304
mux_streambuffer = 65536
sniffer = false
web_port = 0
tls_cert = "<config dir>/cert.pem"   # wss / wssmux only (copy of the tunnel certificate)
tls_key = "<config dir>/key.pem"
log_level = "info"
skip_optz = true
ports = [
  "443=127.0.0.1:443",
  "2053=127.0.0.1:2053",
]
```

Unit: `ExecStart=/var/lib/deyroute/bin/backhaul/v0.7.2/backhaul -c <config dir>/server.toml`,
`WorkingDirectory=<config dir>`.

Binds reported to the port checker and firewall: the control port
(`tcp`, plus `udp` for `backhaul/udp`, purpose `control`) and every user port
on the listen address (purpose `user`). With `accept_udp = true` every
entry of `ports` opens a TCP **and** a UDP listener, so both are reported.

### Node: `client.toml`

```toml
[client]
remote_addr = "5.6.7.8:30001"        # hub public IP : control port
transport = "wssmux"
token = "<tunnel token>"
connection_pool = 8
aggressive_pool = false
keepalive_period = 75
dial_timeout = 10
nodelay = true
retry_interval = 3
mux_version = 1                      # mux_* only for mux transports
mux_framesize = 32768
mux_recievebuffer = 4194304
mux_streambuffer = 65536
sniffer = false
web_port = 0
log_level = "info"
skip_optz = true
```

Unit: `ExecStart=/var/lib/deyroute/bin/backhaul/v0.7.2/backhaul -c <config dir>/client.toml`.
The client binds nothing. It dials the hub and, for every stream, the target
the server sends it (the node's service, e.g. `127.0.0.1:443`).

### Rendering rules

- `bind_addr` is `0.0.0.0:<ctl>`, or `[::]:<ctl>` when the hub public IP the
  clients dial is IPv6 (an IPv4 socket would not accept them).
- `connection_pool` = `advanced.connection_pool` when set (1–1024), otherwise
  `max(8, number of port entries)` (spec 7.1: "based on the number of ports,
  default 8").
- Backend tier (`tuning.backend_tier` for the hub, `nodes[].backend_tier` for
  a node; set by `deyroute optimize auto --backends`): each side uses its own
  tier. `mux_recievebuffer` is 2/4/8 MiB and the server's `channel_size`
  1024/2048/4096 for small/medium/large. Without a tier the values above are
  rendered unchanged (medium is the same).
- `ports` entries: on the default listen address the spec's short form
  `"<listen>=<target>"` (Backhaul then listens on `:<listen>`, every address);
  a specific listen address (`::` for dual stack, `127.0.0.1` for the canary)
  uses `"<addr>:<listen>=<target>"`, e.g. `"[::]:443=127.0.0.1:443"`.
  v0.7.2 only accepts the short form for `1 < port < 65535`, so ports 1 and
  65535 are written as `":<port>=<target>"`.
- A tcp and a udp port map on the same listen port share one entry
  (`accept_udp` starts both listeners from it); their targets must be equal,
  otherwise `Validate` returns `DEY-B006`.
- `accept_udp` is global in v0.7.2: with it **every** entry opens a TCP and
  a UDP listener. `backhaul/tcp` therefore accepts a tunnel with UDP port
  maps only when every listen port is mapped for both protocols; otherwise
  `Validate` returns `DEY-B006` (the extra listener would publish the other
  protocol of the node target, e.g. a loopback-only DNS or admin service, on
  the hub — spec 11). Such tunnels use `backhaul/udp` or `direct/native`.
- `keepalive_period`/`nodelay` are omitted for `backhaul/udp` (its server and
  client ignore them in v0.7.2, and its README sample has neither).
- The canary unit renders only the first port map and always binds it on a
  loopback address.
- `Probe` returns `ErrNoProbe`: the web statistics port stays off (see below).

## Differences between the spec sample (section 7.1) and v0.7.2

| Key / behaviour | Spec sample | Pinned v0.7.2 | What deyroute renders |
| --- | --- | --- | --- |
| `accept_udp` | not shown; table says backhaul carries tcp, udp | `accept_udp` (server) carries UDP over the **tcp** transport only; other TCP-family transports ignore it; when on, every `ports` entry opens both a TCP and a UDP listener | `accept_udp = true` for `backhaul/tcp` when the tunnel has UDP port maps and every listen port is mapped for both protocols (`DEY-B006` otherwise, `false` for TCP-only tunnels); UDP-only tunnels use `backhaul/udp` |
| `skip_optz` | not shown | default `false`: at start Backhaul runs `sysctl -w` (buffers, port range, `tcp_notsent_lowat` …) and raises `RLIMIT_NOFILE` | `skip_optz = true` on both sides: kernel tuning belongs to `deyroute optimize` only (spec 12: only with the owner's confirmation, only in `99-deyroute.conf`); the unit sets `LimitNOFILE` |
| `mux_con` | server | server only (`mux_con`, default 8); the client has no such key | server only |
| `mux_session` | not shown | exists (default 1) but the README describes `mux_con` | not rendered (default) |
| `heartbeat` | 20 | default 40, minimum 1; used by every server transport | 20 |
| `web_port` | `0`; "only on 127.0.0.1 and only if the owner wants stats" | serves on `":<web_port>"` (every interface); there is no web bind-address key | always `0`. `advanced.backhaul_web_port` is refused by config validation (`DEY-C013`) because v0.7.2 cannot keep it on 127.0.0.1 (a public Backhaul dashboard would identify the hub); `Validate` still fails such an input with `DEY-B006` |
| `sniffer_log` | not shown | default `backhaul.json` in the working directory, used only with the sniffer / web port | not rendered (sniffer off) |
| `proxy_protocol` | not shown | server key (PROXY v2 to the target) for tcp, tcpmux, wsmux, wssmux | not rendered; client IP is preserved only by `direct/haproxy` and Xray (spec 10). Every rung of a ladder must deliver the same byte stream to the node service, so a PROXY header on some rungs only would break the others |
| `edge_ip` | not shown | client key for CDN edge IPs (ws transports) | not rendered (direct connection to the hub) |
| `mss`, `so_rcvbuf`, `so_sndbuf`, `pprof` | not shown | optional | not rendered (system defaults; pprof would listen on `0.0.0.0:6060`/`6061`) |
| `ports` listen address | `"443=127.0.0.1:443"` | also `"ip:port=target"`, ranges `"443-600"` | short form on the default address, `"addr:port=target"` otherwise (see above) |
| CLI | — | `backhaul -c <file>` (`-v` prints the version); config hot-reload by polling the file every 2 s | `-c <file>` |

## Security notes

- **wss client does not verify the certificate.** In v0.7.2 the WebSocket
  dialer uses `InsecureSkipVerify: true` (`internal/utils/network/ws_dialer.go`),
  so the node does not check the hub's tunnel certificate. Traffic is still
  encrypted with TLS; authentication is the per-tunnel token (32 random bytes)
  that the server checks on the control channel. An active man-in-the-middle
  between hub and node could terminate TLS and learn the token; the tunnel
  certificate therefore mainly shapes how the connection looks (real TLS, an
  ACME certificate when `hub.domain` is set) rather than authenticating the
  hub. Use `rathole/noise` or `xray/reality` rungs where mutual cryptographic
  authentication matters.
- The server accepts TLS 1.2 and 1.3 (Go `http.ListenAndServeTLS` defaults);
  v0.7.2 has no key to require TLS 1.3 only (spec 10 allows 1.2 when a backend
  forces it).
- The node client dials only what the hub server names; the hub is trusted
  and there is no inbound listener on the node, so the node is never an open
  proxy through Backhaul (spec 11 concerns the Forward backends).
- The token is in the rendered files (`0640 root:deyroute`), never on the
  command line or in the environment.

## Known limitations

- `backhaul/tcp` carries UDP only for tunnels that map both protocols of
  every listen port (see the rendering rules); a tunnel such as
  `443/tcp + 27015/udp` skips this rung with `DEY-B006` and uses the
  UDP-companion rungs (`backhaul/tcpmux` …) instead.
- One listen port cannot forward TCP and UDP to different targets with
  `backhaul/tcp` (`DEY-B006`); `direct/native` can.
- A UDP user datagram larger than 16 KB is not supported by Backhaul's UDP
  buffers.
- Backhaul reloads its config file when its mtime changes; deyroute restarts
  the unit after re-rendering anyway, so the hot reload is not relied upon.
