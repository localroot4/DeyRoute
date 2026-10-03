# Xray-Reality relay backend (`xray/reality`)

| | |
| --- | --- |
| Upstream | [XTLS/Xray-core](https://github.com/XTLS/Xray-core) |
| Pinned version | **v26.3.27** (`internal/backend/backends.yaml`; Go module `github.com/xtls/xray-core v1.260327.0`) |
| Reference | [README at v26.3.27](https://github.com/XTLS/Xray-core/blob/v26.3.27/README.md) and the v26.3.27 sources that parse the JSON config: `infra/conf/xray.go`, `dokodemo.go`, `vless.go`, `freedom.go`, `blackhole.go`, `router.go`, `transport_internet.go` (REALITY), `main/run.go`, `main/commands/all/curve25519.go` |
| Release assets | `Xray-linux-64.zip`, `Xray-linux-arm64-v8a.zip` (`.github/workflows/release.yml` + `.github/build/friendly-filenames.json`), binary `xray`; upstream publishes `<asset>.dgst` digests |
| Package | `internal/backend/xray` |
| Direction | Forward: the node runs the VLESS + REALITY inbound on `<ctl>/tcp`, the hub runs the client and binds the user ports |

## Transport

| Transport id | Carries | Needs UDP between hub and node | Needs tunnel TLS | Stealth | Client IP |
| --- | --- | --- | --- | --- | --- |
| `xray/reality` | tcp, udp | no | no (REALITY has its own X25519 keys) | 5 | masked |

UDP port maps travel as XUDP inside the TCP REALITY connection (Vision turns
UDP requests into XUDP on its own), so the rung works when UDP between the
servers is blocked. Any other protocol fails `Validate` with `DEY-B010`.

## Keys (`GenerateKeys`, pure Go)

Stored in `/etc/deyroute/secrets/backend-keys/<tunnel>/xray.json`:

| Key | Meaning |
| --- | --- |
| `reality_private` | node REALITY X25519 private key, `base64.RawURLEncoding`, clamped exactly like `xray x25519` |
| `reality_public` | the matching public key (hub `realitySettings.password`) |
| `reality_short_id` | REALITY shortId, 8 lowercase hex characters |
| `uuid` | VLESS user id, random UUID v4 |

`Validate` checks that all four exist, decode, and that the public key belongs
to the private key.

## How each side is rendered

Files are written to `/etc/deyroute/backends/xray/<tunnel>/<node>/reality/`;
the unit runs `<bin>/xray run -c <config dir>/config.json` with
`WorkingDirectory` = the config dir and the unchanged hardened template (Xray is
a static Go binary, no hardening option has to be dropped).

### Hub: `config.json` (shortened)

```json
{
  "log": { "loglevel": "warning", "access": "none" },
  "inbounds": [
    { "tag": "in-tcp-443", "listen": "0.0.0.0", "port": 443, "protocol": "dokodemo-door",
      "settings": { "address": "127.0.0.1", "port": 443, "network": "tcp" } },
    { "tag": "in-udp-27015", "listen": "0.0.0.0", "port": 27015, "protocol": "dokodemo-door",
      "settings": { "address": "127.0.0.1", "port": 27015, "network": "udp" } }
  ],
  "outbounds": [
    { "tag": "to-node", "protocol": "vless",
      "settings": { "vnext": [ { "address": "<node ip>", "port": 30001,
        "users": [ { "id": "<uuid>", "encryption": "none", "flow": "xtls-rprx-vision" } ] } ] },
      "streamSettings": { "network": "raw", "security": "reality",
        "realitySettings": { "serverName": "<decoy>", "fingerprint": "chrome",
          "password": "<reality_public>", "shortId": "<reality_short_id>", "spiderX": "/" } },
      "mux": { "enabled": false } }
  ]
}
```

One `dokodemo-door` inbound per port map (`listen` = `RenderInput.ListenAddr`,
`127.0.0.1` for the canary unit). The hub binds exactly these user ports.

### Node: `config.json` (shortened)

```json
{
  "inbounds": [
    { "tag": "vless-in", "listen": "0.0.0.0", "port": 30001, "protocol": "vless",
      "settings": { "clients": [ { "id": "<uuid>", "flow": "xtls-rprx-vision" } ], "decryption": "none" },
      "streamSettings": { "network": "raw", "security": "reality",
        "realitySettings": { "show": false, "target": "<decoy>:443", "xver": 0,
          "serverNames": [ "<decoy>" ], "privateKey": "<reality_private>", "shortIds": [ "<reality_short_id>" ] } } }
  ],
  "outbounds": [
    { "tag": "block", "protocol": "blackhole" },
    { "tag": "direct-tcp-127.0.0.1:443", "protocol": "freedom", "settings": { "redirect": "127.0.0.1:443" } },
    { "tag": "direct-tcp-127.0.0.1:2053", "protocol": "freedom", "settings": { "redirect": "127.0.0.1:2053" } }
  ],
  "routing": { "domainStrategy": "AsIs", "rules": [
    { "ruleTag": "allow-tcp-127.0.0.1:443", "inboundTag": ["vless-in"], "ip": ["127.0.0.1"], "port": "443", "network": "tcp", "outboundTag": "direct-tcp-127.0.0.1:443" },
    { "ruleTag": "allow-tcp-127.0.0.1:2053", "inboundTag": ["vless-in"], "ip": ["127.0.0.1"], "port": "2053", "network": "tcp", "outboundTag": "direct-tcp-127.0.0.1:2053" },
    { "ruleTag": "deny-all", "network": "tcp,udp", "outboundTag": "block" }
  ] }
}
```

The node binds only `<ctl>/tcp` (purpose `control`).

## Node is not an open proxy (spec section 11, scenario S17)

The hub's dokodemo-door writes the final destination into every VLESS request,
so without routing the node would forward to anything the holder of the UUID
asks for. The node config therefore contains:

- one allow rule per distinct target `(host, port, network)` of the tunnel
  (IP literals in `ip`, host names as `domain: ["full:<name>"]`), each routed
  to its **own** `freedom` outbound `direct-<network>-<host:port>` whose
  `settings.redirect` is exactly that target;
- a final catch-all rule to `block`;
- `block` (blackhole) as the **first** outbound, i.e. Xray's default when no
  rule matches.

`8.8.8.8:53` (udp or tcp), other loopback ports and every other host are
dropped.

Why a pinned `freedom` per target instead of one shared `direct` outbound:
Vision carries UDP as XUDP, and XUDP `Keep` frames may name a new
destination for every packet (`common/mux/frame.go`, full-cone). Routing
only sees the first destination of a session, and a plain `freedom` sends
every later packet wherever the frame says, so one allowed UDP session could
reach `8.8.8.8:53` or any other host. With `redirect`, freedom overrides the
address and port of every TCP dial and UDP packet (`UDPOverride` in
`proxy/freedom`), so an allowed session can only reach its own target.
`TestNodeXUDPCannotEscape` covers this. This is stricter than the bound in spec section 11 ("127.0.0.0/8 and
the defined targets"): arbitrary loopback ports are *not* reachable, only the
exact targets. `TestNodeNotOpenProxy` evaluates the rendered rules.

## Differences between the spec sample (section 7.5) and v26.3.27

| Spec sample | Rendered (v26.3.27) | Why |
| --- | --- | --- |
| `dokodemo-door` | `dokodemo-door` | v26.3.27 also accepts the new name `tunnel` for the same inbound; the spec name is kept |
| `network: tcp` or `tcp,udp` per inbound | one inbound per port map with `network: "tcp"` or `"udp"` | port maps are per protocol; a tcp and a udp map on the same port become two inbounds |
| stream `network` (not given) | `"raw"` | `raw` is the current name of the TCP transport (`tcp` is an alias) |
| `realitySettings.dest` | `realitySettings.target` | `target` is the v26 name; `dest` is still accepted as an alias |
| `realitySettings.publicKey` (client) | `realitySettings.password` | v26 names the client public key `password` (`publicKey` remains an alias; `xray x25519` prints "Password (PublicKey)") |
| `realitySettings.shortIds` on the client | `shortId` (single) | the client side rejects `shortIds` ("please use shortId instead") |
| `flow: xtls-rprx-vision` | same; hub uses `xtls-rprx-vision-udp443` only when a UDP port map targets port 443 | Vision rejects UDP/443 (QUIC) unless the `-udp443` flow variant is used; the node keeps plain `xtls-rprx-vision` |
| inbound client `encryption` | not rendered on the node | Xray v26 rejects `encryption` inside inbound `clients` ("should not be in inbound settings") |
| Mux off | `"mux": {"enabled": false}` | Mux is incompatible with Vision; XUDP still carries UDP |
| outbound `freedom` | `blackhole` (tag `block`) first, then one `freedom` with `redirect: "<target>"` per allowed target, plus routing | security requirement of section 7.5 / 11 (see above: XUDP per-packet destinations) |
| keys via `xray x25519` | pure Go X25519 (`crypto/ecdh`), same clamping and encoding | exec allow-list (QUESTIONS.md C.15) |
| `log` not given | `loglevel: warning`, `access: none` | the access log would record every user connection |

## Known limitations

- REALITY on a port other than 443 makes Xray print a warning at start
  ("Listening on non-443 ports may get your IP blocked"); the node listens on
  the allocated control port by design (30000-31999).
- Choosing Apple/iCloud as decoy triggers another upstream warning.
- With `www.microsoft.com` as the decoy the rung failed the ladder test
  (`test-ladder`) on every lab run, although the site offers TLS 1.3, X25519
  and HTTP/2; with `dl.google.com` it passes. It is no longer in the built-in
  list (`internal/backend/decoy.go`).
- Decoy: `RenderInput.Decoy` (the reachable decoy the hub selected), else the
  first `hub.decoy_snis` entry, else the first built-in decoy
  (`backend.DecoyFor`); an IP or a malformed name fails `Validate` with
  `DEY-B006`.
- The node service sees the node's own address, not the user's IP
  (`acceptProxyProtocol` is not used).
- Targets that are host names are matched by name (`full:`); a request that
  names the same service by IP address is blocked.
