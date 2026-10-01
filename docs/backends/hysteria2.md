# Hysteria 2 backend (`hysteria2/udp`)

| | |
| --- | --- |
| Upstream | [apernet/hysteria](https://github.com/apernet/hysteria) |
| Pinned version | **app/v2.12.3** (`internal/backend/backends.yaml`; Go module `github.com/apernet/hysteria/app/v2 v2.12.3`, the latest tag when pinned) |
| Reference | [README at app/v2.12.3](https://github.com/apernet/hysteria/blob/app/v2.12.3/README.md) and the config structs of that tag: `app/cmd/server.go`, `app/cmd/client.go`, `app/cmd/root.go`, `extras/outbounds/acl/*.go` (ACL syntax), `app/internal/utils/bpsconv.go`, `app/internal/utils/certloader.go` |
| Release assets | raw executables `hysteria-linux-amd64`, `hysteria-linux-arm64` (`hyperbole.py`, `platforms.txt`); the tag contains a slash, so the download path is `app%2Fv2.12.3` |
| Package | `internal/backend/hysteria2` |
| Direction | Forward: the node runs `hysteria server` on `<ctl>/udp`, the hub runs `hysteria client` which binds the user ports |

## Transport

| Transport id | Carries | Needs UDP between hub and node | Needs tunnel TLS | Stealth | Client IP |
| --- | --- | --- | --- | --- | --- |
| `hysteria2/udp` | tcp, udp | **yes** (QUIC) | no (own node certificate, below) | 3 | masked |

The rung is only used after the UDP probe (spec section 10) passed; when UDP
is blocked it is skipped with `DEY-B007` and a yellow event (S16).

## Keys (`GenerateKeys`, pure Go)

| Key | Meaning |
| --- | --- |
| `obfs_password` | Salamander obfuscation password: 32 random bytes, `base64.RawURLEncoding` (43 characters). It is separate from the tunnel token, which is the auth password. |
| `node_cert_pem`, `node_key_pem` | The node's own self-signed ECDSA P-256 certificate and key (20 years), written only on the node as `node-cert.pem` / `node-key.pem`. |
| `node_cert_sha256` | sha256 of that certificate (hex); the hub pins it (`pinSHA256`). |

The node is the TLS server here, so it serves a certificate of its own: the
tunnel's certificate key (internal CA, ACME or the owner's custom key) never
leaves the hub. Keys created before this change are completed automatically
on the next render (stored keys are kept, missing ones are added).

Other secrets come from the tunnel: `Secrets.Token` (auth) and
`Secrets.ServerName` (SNI).

## How each side is rendered

Files are written to `/etc/deyroute/backends/hysteria2/<tunnel>/<node>/udp/`.
Units:

- node: `<bin>/hysteria server -c <dir>/server.yaml --disable-update-check`
- hub: `<bin>/hysteria client -c <dir>/client.yaml --disable-update-check`

`--disable-update-check` stops the daily request to `api.hy2.io`. The hardened
template is used unchanged (static Go binary).

### Node: `server.yaml`

```yaml
listen: ":30001"
tls:
  cert: "<dir>/node-cert.pem"
  key: "<dir>/node-key.pem"
  sniGuard: "disable"
auth:
  type: "password"
  password: "<tunnel token>"
obfs:
  type: "salamander"
  salamander:
    password: "<obfs_password>"
disableUDP: true          # false when the tunnel has UDP port maps
speedTest: false
acl:
  inline:
    - "direct(127.0.0.1, tcp/2053, 127.0.0.1)"
    - "direct(127.0.0.1, tcp/443, 127.0.0.1)"
    - "reject(all)"
masquerade:
  type: "404"
```

The node binds `<ctl>/udp` (purpose `control`). With
`advanced.hysteria_port_hopping: true` the node side also returns
`Rendered.NAT = [udp 20000-20999 -> <ctl>]` (local redirect) for the
`inet deyroute` table.

### Hub: `client.yaml`

```yaml
server: "<node ip>:30001"         # "<node ip>:20000-20999" with port hopping
auth: "<tunnel token>"
obfs:
  type: "salamander"
  salamander:
    password: "<obfs_password>"
tls:
  sni: "<Secrets.ServerName>"
  insecure: true
  pinSHA256: "<sha256 of the node certificate, hex>"
bandwidth:
  up: "100 mbps"                  # advanced.hysteria_up_mbps
  down: "100 mbps"                # advanced.hysteria_down_mbps
fastOpen: true
tcpForwarding:
  - listen: "0.0.0.0:443"
    remote: "127.0.0.1:443"
udpForwarding:
  - listen: "0.0.0.0:27015"
    remote: "127.0.0.1:27015"
```

The hub binds every user port (tcp and udp) on `RenderInput.ListenAddr`
(`127.0.0.1` for the canary unit).

## Node is not an open proxy (spec section 11, scenario S17)

The server ACL contains one `direct(<host>, <proto>/<port>)` rule per distinct
target of the tunnel, followed by `reject(all)`. Rules are evaluated in order
by the ACL engine of `extras/outbounds`. Because an ACL is present, the server
puts the system resolver in front of it, so host-name targets are matched by
name. `8.8.8.8:53` and every other destination are rejected. As with Xray,
only the exact targets are reachable, which is stricter than the
"127.0.0.0/8 and the targets" bound of section 11.

Rules for IP-literal targets carry the same IP as **hijack address**
(`direct(127.0.0.1, tcp/443, 127.0.0.1)`). The resolver also resolves
host-name requests, and an IP matcher matches a name whose first A *or* AAAA
record is that IP (`acl/matchers.go`). The direct outbound in auto mode then
dials both resolved addresses (happy eyeballs). Without the hijack, a name
with `A=127.0.0.1` and `AAAA=<anything>` would reach `<anything>:443`. The
hijack rewrites the destination to exactly the allowed IP.
`TestServerNotOpenProxy` parses the rules with the upstream line syntax and
evaluates them. `TestACLHostNameCannotEscape` models the resolver and the
hijack.

Residual (upstream behaviour, authenticated peers only): for UDP, hysteria
checks each new destination with the ACL but then sends the packet with a
fresh `AddrEx`, so the direct outbound resolves a host-name destination again
and the hijack does not apply to UDP writes. A peer that knows the tunnel
token and the obfuscation password could therefore use DNS rebinding (a name
answering `127.0.0.1` to the ACL check and another IP to the second lookup)
to send UDP to the target *port* on another host. Only the hub holds these
credentials, and the port is still limited to a configured target port.

## Differences between the spec sample (section 7.6) and app/v2.12.3

| Spec sample | Rendered (app/v2.12.3) | Why |
| --- | --- | --- |
| `auth.password` | `auth: {type: password, password: …}` | the server needs the auth `type` |
| client `auth` (not detailed) | top-level string `auth: "<token>"` | client auth is a plain string in v2 |
| `obfs.salamander` | `obfs: {type: salamander, salamander: {password: …}}` on both sides | v2 layout |
| `tls.pinSHA256` | `tls: {insecure: true, pinSHA256: <hex>}` | the pin is checked in `VerifyPeerCertificate`; without `insecure` the normal chain check would also run and fail for the self-signed node certificate |
| TLS with the node's own cert | plus `sniGuard: "disable"` | the default SNI guard (`dns-san`) rejects an SNI that is not a DNS SAN of the certificate; the client authenticates the server by pin and the server authenticates the client by password |
| `masquerade` optional | `masquerade: {type: "404"}` | explicit form of the upstream default |
| `bandwidth: {up, down}` in Mbps | strings `"<n> mbps"` | `StringToBps` format |
| `tcpForwarding: [{listen: 0.0.0.0:443, remote: 127.0.0.1:443}]` | same keys, one entry per tcp port map; `udpForwarding` per udp port map | — |
| port hopping `node_ip:20000-20999` | `server: "<ip>:20000-20999"` + node DNAT rule | a `-` in the port selects the UDP hop client |
| (not in spec) | `disableUDP: true` when the tunnel has no UDP ports, `speedTest: false` | reduce what the server accepts |
| (not in spec) | `acl.inline` allow-list | security requirement of section 11 |

## Known limitations

- Needs UDP between hub and node; the rung is skipped while the UDP probe
  fails or has not run yet, and re-tested every 30 minutes.
- With port hopping the node redirects all external UDP to ports
  20000-20999; do not run other public UDP services in that range on the node.
- The node service sees the node's own address, not the user's IP.
- `udpForwarding` uses the upstream default session timeout (60 s).
