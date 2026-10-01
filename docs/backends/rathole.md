# Rathole backend (`rathole/*`)

| | |
| --- | --- |
| Upstream | [rapiz1/rathole](https://github.com/rapiz1/rathole) |
| Pinned version | **v0.5.0** (`internal/backend/backends.yaml`) |
| Reference | [README at v0.5.0](https://github.com/rapiz1/rathole/blob/v0.5.0/README.md), [docs/transport.md at v0.5.0](https://github.com/rapiz1/rathole/blob/v0.5.0/docs/transport.md), `examples/{noise_nk,tls,udp}` and the v0.5.0 source (`src/config.rs`, `src/cli.rs`, `src/lib.rs`, `src/transport/{noise,tls}.rs`, `.github/workflows/release.yml`) |
| Release assets | `rathole-x86_64-unknown-linux-gnu.zip` (amd64) and `rathole-aarch64-unknown-linux-musl.zip` (arm64), binary `rathole` — names from the release workflow (`rathole-${{ matrix.target }}.zip`). There is **no** x86_64 musl asset. |
| Package | `internal/backend/rathole` |
| Direction | Reverse for every transport: the hub runs `[server]`, the node runs `[client]` |

## Transports

| Transport id | Carries | Needs UDP between hub and node | Needs tunnel TLS | Stealth | Notes |
| --- | --- | --- | --- | --- | --- |
| `rathole/noise` | tcp, udp | no | no | 3 | `Noise_NK_25519_ChaChaPoly_BLAKE2s`, per-tunnel X25519 key (rung 3 of the default ladder) |
| `rathole/tls` | tcp, udp | no | yes (PKCS#12 + CA) | 3 | OpenSSL TLS through `native-tls` |
| `rathole/tcp` | tcp, udp | no | no | 1 | no encryption at all; rathole handshake and user bytes in the clear |

Stealth: the section 7 table only gives `noise:3`. `tls` is a standard TLS
session and hides the rathole protocol as well as Noise does, so it is 3;
`tcp` sends the rathole handshake unencrypted and is trivially fingerprinted,
so it is 1 like `backhaul/tcp` and `direct/native`.

UDP port maps are rathole `type = "udp"` services; their datagrams travel over
the TCP data channel, so no rathole transport needs UDP between hub and node.

## How each side is rendered

Files are written to `/etc/deyroute/backends/rathole/<tunnel>/<node>/<transport>/`
(`0750` directory, `0640` files, `root:deyroute`); the unit is the
`deyroute-tun@<tunnel>.<node>.rathole-<transport>` instance of the shared
hardened template.

### Hub: `server.toml`

```toml
[server]
bind_addr = "0.0.0.0:30001"            # the per-(tunnel, node, transport) control port
default_token = "<tunnel token>"
heartbeat_interval = 20

[server.transport]
type = "noise"                         # "tls" | "tcp"

[server.transport.noise]               # noise only
pattern = "Noise_NK_25519_ChaChaPoly_BLAKE2s"
local_private_key = "<noise_private>"

# [server.transport.tls]               # tls only
# pkcs12 = "<config dir>/tls.p12"
# pkcs12_password = "<generated password>"

[server.services.tcp-443]              # one service per port map
type = "tcp"
bind_addr = "0.0.0.0:443"              # <listen addr>:<listen>

[server.services.udp-27015]
type = "udp"
bind_addr = "0.0.0.0:27015"
```

Unit: `ExecStart=<bin>/rathole --server <config dir>/server.toml`,
`WorkingDirectory=<config dir>`, `Environment=RUST_LOG=info`.
Binds: `tcp 0.0.0.0:<ctl>` (control) and every listen port with its protocol
on the listen address (user).

### Node: `client.toml`

```toml
[client]
remote_addr = "5.6.7.8:30001"          # hub public IP : control port
default_token = "<tunnel token>"
heartbeat_timeout = 40                 # must be > server heartbeat_interval
retry_interval = 3

[client.transport]
type = "noise"

[client.transport.noise]               # noise only
pattern = "Noise_NK_25519_ChaChaPoly_BLAKE2s"
remote_public_key = "<noise_public>"

# [client.transport.tls]               # tls only
# trusted_root = "<config dir>/ca.crt" # internal CA; tls.mode custom: "<config dir>/tls-cert.pem"
# hostname = "<server name>"           # domain or hub IP (omitted when unknown)

[client.services.tcp-443]
type = "tcp"
local_addr = "127.0.0.1:443"           # the port map target
```

Unit: `ExecStart=<bin>/rathole --client <config dir>/client.toml`. The node
binds nothing.

### Rendering rules

- Service names are `<proto>-<listen>` on both sides (rathole matches server
  and client services by name). A `(proto, listen)` pair listed twice is
  `DEY-B006`.
- An empty target defaults to `127.0.0.1:<listen>`; an empty proto to `tcp`.
- `ListenAddr` (`0.0.0.0` by default, `::` for dual stack) is the address of
  every `server.services.*.bind_addr`; IPv6 literals are bracketed.
- Canary (`RenderInput.Canary`): only the first port map is rendered and its
  `bind_addr` is forced to a loopback address.
- Keys (`GenerateKeys`, pure Go `crypto/ecdh` X25519 + `crypto/rand`),
  persisted in `secrets/backend-keys/<tunnel>/rathole.json`:

  | Key | Meaning |
  | --- | --- |
  | `noise_private` | hub static X25519 private key, standard base64 (with padding) of the raw 32 bytes — the encoding `rathole --genkey` prints (`base64::encode(keypair.private)` in `src/lib.rs`) |
  | `noise_public` | the matching public key, same encoding |

  The keys are generated for every rathole transport, so a later switch to
  `rathole/noise` needs no new material. `Validate` decodes both, checks they
  are 32 bytes and that the public key belongs to the private key. The unit
  test checks the encoding against the two keypairs published in the v0.5.0
  documentation and examples.
- `trusted_root` by `tunnels[].tls.mode`. rathole v0.5.0 (native-tls on
  OpenSSL) *adds* the first certificate of `trusted_root` to the system
  trust store; it does not replace it (checked with the real binary:
  with `SSL_CERT_FILE` pointing at a CA, a certificate from that CA verifies
  even though `trusted_root` is another CA). Therefore:

  | `tls.mode` | `trusted_root` | Result |
  | --- | --- | --- |
  | `auto` (or empty) | `ca.crt` (internal CA) | the internal tunnel certificate verifies through the CA |
  | `acme` | `ca.crt` | the Let's Encrypt certificate verifies through the system store; an ACME fallback to an internal certificate still verifies through the CA |
  | `custom` | `tls-cert.pem` (the served certificate, public, copied to the node by the planner) | a self-signed custom certificate verifies as its own anchor (checked with the real binary); one from a public CA verifies through the system store |
- Ids in the header comment are sanitised (no control characters), so a
  corrupted id cannot inject TOML lines.
- `Validate` returns `DEY-B006` for: missing token, hub IP, binary or config
  dir; missing Noise keys (noise); missing `tls.p12`, its password or the CA
  (tls); bad or duplicate port maps. A proto other than tcp/udp is
  `DEY-B010`.

## Differences between the spec sample (section 7.2) and v0.5.0

| Spec sample | Real v0.5.0 | What deyroute renders |
| --- | --- | --- |
| `retry_interval = 3` under `[client]` | exists (`ClientConfig.retry_interval`, default 1) | `retry_interval = 3` |
| no `heartbeat_timeout` | `[client] heartbeat_timeout` (default 40) must be greater than `[server] heartbeat_interval` | `heartbeat_timeout = 40` (spec interval 20) |
| client services without `type` | `type` defaults to `"tcp"` | `type` always rendered (`"tcp"` / `"udp"`) so UDP services match |
| no `pattern` on the client | `pattern` is optional on both sides (default NK) | rendered on both sides |
| key via `rathole --genkey` | same X25519 raw keys, standard base64 | generated in Go (QUESTIONS.md C.15); identical encoding |
| PKCS#12 for `tls` | `[server.transport.tls] pkcs12` + `pkcs12_password` (both required); client `trusted_root` + optional `hostname` | `tls.p12` from `internal/tlsutil` (SHA-1/3DES PBE, SHA-1 MAC) with a password; client pins the internal CA |
| `rathole <config>` (mode auto-detected) | `--server` / `--client` force the mode | `rathole --server server.toml` / `rathole --client client.toml` |
| — | v0.5.0 also has a `websocket` transport | not offered (not in the spec) |

Every config struct in v0.5.0 is `deny_unknown_fields`, so an unknown key
stops rathole at start; the rendered keys were checked against
`src/config.rs`.

## Verification done for this renderer

- rathole v0.5.0 was built from the published crate (`rathole-0.5.0.crate`,
  default features; only the `time` dependency was bumped so today's rustc
  compiles it) and run with the golden `noise_mixed`, `tls_mixed` and `tcp`
  configs on loopback: TCP and UDP echo passed through every transport.
- `rathole/tls` was also run with a CA and tunnel certificate from
  `internal/tlsutil` (Ed25519 CA, ECDSA P-256 leaf with the hub IP as SAN) and
  the bundle from `tlsutil.EncodePKCS12` against OpenSSL 3.0: handshake,
  IP-SAN verification (`hostname = "<hub IP>"`) and traffic succeeded.

- Review pass: every golden config was started with the v0.5.0 binary
  (`--server` / `--client`) and none was rejected by the strict config
  parser (`deny_unknown_fields`; an injected unknown table is rejected).

## Security notes

- Reverse backend: the node client only dials the fixed `local_addr` of each
  service and binds no port, so it cannot become an open proxy (spec 11).
  The server binds only the tunnel's listen ports; there is no dashboard.
- `rathole/tls` is not a strict pin to the internal CA: because native-tls
  keeps the system trust store, a publicly trusted certificate for the hub
  IP or domain would also be accepted. An attacker would need such a
  certificate *and* the tunnel token to impersonate the hub; Noise (the
  default rathole rung) pins the hub's static key exactly.
- `tls.mode custom` with a certificate from a private CA (not self-signed,
  not publicly trusted) cannot verify on the node: native-tls reads only the
  first certificate of `trusted_root` and OpenSSL does not accept a partial
  chain. Use a self-signed or publicly trusted custom certificate.
- The token is rathole's `default_token` (never sent in clear: rathole
  authenticates with a nonce digest). With `rathole/tcp` the user bytes are not
  encrypted by the tunnel (user TLS still protects them end to end).
- Files are `0640 root:deyroute`; the private Noise key and the PKCS#12 password
  only appear in the hub's `server.toml`, the node gets only the public key.

## Hardening exception

`MemoryDenyWriteExecute` is dropped for rathole units
(`UnitSpec.DropHardening`): the v0.5.0 release workflow compresses the Linux
binaries with UPX, whose loader unpacks the code into anonymous memory and
makes it executable — `MemoryDenyWriteExecute` forbids exactly that and the
process would be killed by seccomp at start. Every other option of the shared
template stays.

## Known limitations

- **amd64 asset needs glibc and OpenSSL 3.** v0.5.0 builds x86_64 only for
  `x86_64-unknown-linux-gnu` on `ubuntu-latest` of October 2023 (Ubuntu 22.04),
  dynamically linked to glibc and `libssl.so.3`. It runs on Ubuntu 22.04+,
  Debian 12+, Rocky/Alma 9+, Fedora and Arch; on Ubuntu 20.04, Debian 11 and
  Rocky/Alma 8 (Tier 2) it will not start (missing `libssl.so.3`/newer glibc),
  so the unit fails and failover moves on. A static musl build published on
  the owner's mirror would remove this (QUESTIONS.md C.38).
- **arm64 has no TLS.** The aarch64 asset is built with
  `--features embedded --no-default-features` (server, client, noise,
  hot-reload): `rathole/noise` and `rathole/tcp` work, `rathole/tls` exits at
  start with "feature not compiled" on arm64 hubs/nodes. The renderer is pure
  and does not know the architecture, so the failure shows up as a unit start
  failure (`DEY-B003`) and the rung is quarantined.
- rathole watches its config file (`hot-reload` feature); deyroute always
  restarts the unit after a re-render anyway.
- The TLS version is not configurable in rathole v0.5.0; native-tls on
  OpenSSL 3 negotiates TLS 1.3 between hub and node (spec 10), but the
  server would also accept TLS 1.2.
