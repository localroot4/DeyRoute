# Waterwall backend (`waterwall/reverse-reality`)

| | |
| --- | --- |
| Upstream | [radkesvat/WaterWall](https://github.com/radkesvat/WaterWall) |
| Pinned version | **v1.46.94** (`internal/backend/backends.yaml`; latest tag when pinned — see "Why not v1.41" below) |
| Reference | [readme at v1.46.94](https://github.com/radkesvat/WaterWall/blob/v1.46.94/readme.md), the node references of that tag (`tunnels/{RealityServer,RealityClient,ReverseServer,ReverseClient,Bridge,TcpListener,TcpConnector,HeaderClient,HeaderServer}/description.md`, mirrored in the official docs repo WaterWall-Docs `docs/02-noderefs`), the official reverse and Reality configs in `tests/cases/reverse_tcp_bridge_roundtrip/config.json` and `tests/cases/reality_google_roundtrip/config.json`, and `docs/01-getting-started/{installation,tutorial-part1}.mdx` of WaterWall-Docs for `core.json` |
| Release assets | `Waterwall-linux-gcc-x64.zip`, `Waterwall-linux-gcc-arm64.zip` (`.github/workflows/ci.yaml`: `Waterwall-<cmake preset>` artifacts zipped by the release job), binary `Waterwall` |
| Package | `internal/backend/waterwall` |
| Direction | Reverse: the hub accepts the node's Reality connections on `<ctl>/tcp` and binds the user ports; the node dials the hub |

## Why not v1.41

The manifest originally pinned v1.41. At every tag from v1.33 to v1.43 the
Reality nodes are not built (`option(INCLUDE_REALITY_SERVER … FALSE)` in
`CMakeLists.txt`) and their sources (`tunnels/server/reality`,
`tunnels/client/reality`) are absent, so `reverse-reality` cannot run on those
releases. v1.32 is the last old-series tag with Reality, but its TCP
multi-port listener either flushes the whole `iptables -t nat` table
(`iptables` backend) or dereferences a null listener (`socket` backend) and
misses the last port of the range. Reality v2 returns in the v1.44.x series;
v1.46.94 is the newest tag and the one the current official documentation
describes, so it is pinned.

## Transport

| Transport id | Carries | Needs UDP between hub and node | Needs tunnel TLS | Stealth | Client IP |
| --- | --- | --- | --- | --- | --- |
| `waterwall/reverse-reality` | tcp | no | no (own password) | 5 | masked |

UDP port maps fail `Validate` with `DEY-B010`.

## Keys (`GenerateKeys`, pure Go)

| Key | Meaning |
| --- | --- |
| `password` | shared Reality secret of `RealityServer`/`RealityClient`: 24 random characters from `[A-Za-z0-9]` (Waterwall accepts 1..32 bytes) |

## How each side is rendered

Files are written to
`/etc/deyroute/backends/waterwall/<tunnel>/<node>/reverse-reality/`:
`core.json` and `config.json`. Waterwall takes no arguments and reads
`./core.json`, so the unit is `ExecStart=<bin>/Waterwall` with
`WorkingDirectory` = that directory (spec 7.4).

### Hub graph (`config.json`)

```text
users-inbound  TcpListener {address: <ListenAddr>, port: [443, 2053], nodelay}
  -> header-client  HeaderClient {data: "src_context->port"}      (several ports only)
  -> bridge-users   Bridge {pair: bridge-reverse}
node-inbound   TcpListener {address: 0.0.0.0, port: <ctl>, nodelay, whitelist: [<node ip>/32]}
  -> reality-server RealityServer {destination: reality-decoy, password}
  -> reverse-server ReverseServer {}
  -> bridge-reverse Bridge {pair: bridge-users}
reality-decoy  TcpConnector {address: <decoy>, port: 443, nodelay}
```

### Node graph (`config.json`)

```text
bridge-service  Bridge {pair: bridge-reverse}
  -> header-server    HeaderServer {override: "dest_context->port"}   (several ports only)
  -> service-outbound TcpConnector {address: 127.0.0.1, port: "dest_context->port" | <target port>}
bridge-reverse  Bridge {pair: bridge-service}
  -> reverse-client   ReverseClient {minimum-unused: 16}
  -> reality-client   RealityClient {sni: <decoy>, password}
  -> hub-outbound     TcpConnector {address: <hub ip>, port: <ctl>, nodelay}
```

About the "official reverse reality example" (spec 7.4): the official docs
repository has an Examples section (`WaterWall-Docs/docs/03-examples/`,
"Configuration examples, from simple chains to more advanced layouts"). This
build environment could only read files by exact path (no directory listing),
and no reverse-reality page was found under the names tried. The graph is
therefore assembled from official material of the pinned tag, listed in the
header table. Owner action: compare `config.json` with that example page and
report any difference (QUESTIONS.md C.40).

This is the official reverse layout (`ReverseServer -> Bridge` and
`Bridge -> ReverseClient` directly adjacent, as the Bridge/Reverse references
require) with the Reality pair inserted on the transport side, and the
official "preserve the accepted listener port" HeaderClient/HeaderServer
pattern for several user ports. Only the hub IP and control port, the user
ports, the password and the SNI/decoy are substituted.

Ports:

- **one port map**: no header; `service-outbound` connects to the port map's
  target (so `443 -> 127.0.0.1:8443` works). The canary unit always uses this
  form (`127.0.0.1:<listen>` on the hub).
- **several port maps**: one `TcpListener` with an explicit `port` array
  (socket per port), the listener port travels in the 2-byte header and the
  node connects to `<host>:<that port>`. Therefore every target must be on the
  same host and use the same port as its listen port; otherwise `Validate`
  returns `DEY-B006` (use one tunnel per differing target).

The hub binds `<ctl>/tcp` (purpose `control`) and every user port on
`RenderInput.ListenAddr` (purpose `user`); the node binds nothing.

### `core.json`

```json
{
  "log": {
    "path": "/tmp/deyroute-waterwall/",
    "internal": { "loglevel": "INFO", "file": "internal.log", "console": true },
    "core":     { "loglevel": "INFO", "file": "core.log",     "console": true },
    "network":  { "loglevel": "INFO", "file": "network.log",  "console": true },
    "dns":      { "loglevel": "INFO", "file": "dns.log",      "console": true }
  },
  "misc": { "workers": 4, "ram-profile": "server", "mtu": 1500, "tcp-tune": false, "try-enabling-bbr": false },
  "configs": [ "config.json" ]
}
```

- `loglevel` is `DEBUG` for every logger when `RenderInput.FirstRun` is set
  (spec 7.4), `INFO` afterwards.
- Every logger also writes to the console, i.e. into
  `/var/log/deyroute/tunnels/<tunnel>.log`, where the last 40 lines are taken for
  `DEY-B043`. Waterwall always writes log files too; they go to the unit's
  `PrivateTmp` because the config directory is read-only under
  `ProtectSystem=strict`.
- `tcp-tune` and `try-enabling-bbr` are off: deyroute owns kernel tuning
  (spec section 12).
- `workers`: `min(4, CPU)` of the side that runs the unit. The hub passes
  its own CPU count and each node's (reported in the node's hello) in
  `RenderInput.HubCPUs` / `NodeCPUs`; while a node's count is unknown
  `DefaultWorkers` (4) is rendered.

### Pre-start validation

The backend's `PreStart` hook (run by the hub and the node daemon right
before `systemctl start`) reads the `*.json` files of the config directory
and runs `waterwall.ValidateJSON`:
`DEY-B041` when `core.json` is missing, `DEY-B040` (file + reason in the
error detail) when any file is not valid JSON, `core.json` lists a missing
config, or a graph is inconsistent (duplicate/missing names, unknown `next`,
two nodes chained to the same node, unknown Bridge `pair` or RealityServer
`destination`).

### Hardening

No option of the hardened template is dropped. The pinned Linux x64 release
is a plain dynamically linked ELF (not packed) and runs under
`MemoryDenyWriteExecute` (checked with `systemd-run -p
MemoryDenyWriteExecute=yes -p NoNewPrivileges=yes -p
SystemCallFilter=@system-service` on systemd 255: the reverse-reality hub
config reaches `active` and listens).

## Differences between the spec sample (section 7.4) and v1.46.94

| Spec sample | Rendered (v1.46.94) | Why |
| --- | --- | --- |
| pinned v1.41 | v1.46.94 | v1.41 has no Reality nodes (see above) |
| `RealityServer` with "one real SNI as decoy" | `RealityServer {destination: <TcpConnector to decoy:443>, password}` | Reality v2: the decoy is a `destination` node, the SNI is set on the client (`RealityClient.sni`) |
| `ReverseServer` "takes the user ports" | user `TcpListener -> Bridge <=> Bridge <- ReverseServer` | Bridge pair is mandatory between ReverseServer and the user branch |
| node: `ReverseClient -> RealityClient -> TcpConnector` | same, preceded by `Bridge (pair) -> ReverseClient` | Bridge adjacency rule |
| final `TcpConnector` to `127.0.0.1:<port>` | same; with several ports `HeaderClient`/`HeaderServer` + `port: "dest_context->port"` | one ReverseServer chain carries every port |
| `core.json` `ram-profile: server`, threads = min(4, CPU) | `misc.ram-profile`, `misc.workers` (min(4, CPU) of the side, see above) | key names of `misc` |
| `log-level: debug` on first run | `log.<logger>.loglevel: "DEBUG"` | per-logger levels in v1.46 |

## Known limitations

- **glibc 2.34.** The pinned x64 and arm64 assets are linked against glibc
  2.34: on Ubuntu 20.04, Debian 11 and Rocky/Alma 8 (Tier 2) Waterwall exits
  at start ("GLIBC_2.34 not found"), the unit fails with `DEY-B043` and the
  failover uses the other rungs (QUESTIONS.md C.38).
- TCP only.
- **Client-speaks-first protocols only.** `ReverseServer` pairs a user
  connection with a reverse link only when the user sends the first bytes
  (`tunnels/ReverseServer/downstream/payload.c`). With several ports the
  `HeaderClient` port header is also sent with the first client payload. A
  service where the server talks first (SSH banner, SMTP, MySQL, …) therefore
  never gets connected over this rung. HTTPS/TLS, HTTP and VLESS/VMess/Trojan
  panels are client-first and work. The path probe (`auto` sends a TLS
  ClientHello) is not affected.
- Decoy: `RenderInput.Decoy`, else the first `hub.decoy_snis` entry, else the
  first built-in placeholder (`backend.DecoyFor`).
- Several port maps must share one target host and keep the listen port.
- `ReverseClient` keeps a pool of idle, pre-opened connections to the hub (`minimum-unused: 16`).
- Reality v2 is wire-incompatible with other Waterwall versions: hub and node
  must run the same pinned binary (the installer guarantees this).
