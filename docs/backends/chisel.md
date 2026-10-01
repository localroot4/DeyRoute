# Chisel backend (`chisel/wss`, optional)

| | |
| --- | --- |
| Upstream | [jpillora/chisel](https://github.com/jpillora/chisel) |
| Pinned version | **v1.12.1** (`internal/backend/backends.yaml`) |
| Reference | [README at v1.12.1](https://github.com/jpillora/chisel/blob/v1.12.1/README.md) (server/client `--help`, Security, Authentication, TLS guide) and the v1.12.1 sources: `main.go` (server/client options, `AUTH` fallback), `server/server.go` (`--keyfile`, `--authfile`, fingerprint), `share/settings/{remote,users,user}.go` (remote syntax, `UserAddr`, authfile format), `share/ccrypto/keys.go` (`FingerprintKey`), `client/client.go` (`--tls-ca`, `--sni`, fingerprint check) |
| Release assets | upstream tagged v1.12.1 without publishing assets (v1.12.0 lacks the reverse-UDP return-peer fix this backend needs for `/udp` maps), so `scripts/build-backends.sh` builds the tagged module exactly like upstream's `.github/goreleaser.yml` (CGO off, `-trimpath`, `-s -w`, `share.BuildVersion`) with a pinned Go toolchain; the reproducible raw binaries `chisel-v1.12.1-linux-{amd64,arm64}` are published on the DEYROUTE release `backend-builds` (QUESTIONS.md C.29), binary `chisel` |
| Package | `internal/backend/chisel` |
| Direction | Reverse: the hub runs `chisel server --reverse`, the node runs `chisel client` |

## Transport

| Transport id | Carries | Needs UDP between hub and node | Needs tunnel TLS | Stealth | Optional |
| --- | --- | --- | --- | --- | --- |
| `chisel/wss` | tcp, udp | no | yes | 3 | yes (section 7.9) |

On the wire: HTTPS to `<hub>:<ctl>`, a WebSocket upgrade, and SSH inside it.
UDP remotes (`/udp`) travel inside the same SSH session.

## How each side is rendered

Hub files (`/etc/deyroute/backends/chisel/<tunnel>/<node>/wss/`, `0640 root:deyroute`):

* `users.json` — the `--authfile`:
  ```json
  { "dey:7272b276741958c4c32917de31c25e40": ["^R:0\\.0\\.0\\.0:443$", "^R:0\\.0\\.0\\.0:2053$"] }
  ```
  One anchored pattern per listen port (chisel v1.12.1 does not anchor
  patterns itself). The server matches reverse remotes as
  `R:<local-host>:<local-port>` (no protocol), so one pattern covers the
  tcp and udp maps of a port.
* `server.key` — the SSH host key (`--keyfile`, PEM `EC PRIVATE KEY`).

Hub unit:

```
chisel server --host 0.0.0.0 --port 30001 --reverse \
  --keyfile <dir>/server.key --authfile <dir>/users.json \
  --tls-key <dir>/tls-key.pem --tls-cert <dir>/tls-cert.pem --keepalive 25s
```

Node unit (no files):

```
AUTH=dey:<password>   (unit Environment)
chisel client --fingerprint <fp> --tls-ca <dir>/ca.crt [--sni <server name>] \
  --keepalive 25s --max-retry-interval 10s https://5.6.7.8:30001 \
  R:0.0.0.0:443:127.0.0.1:443 R:0.0.0.0:443:127.0.0.1:443/udp …
```

Binds: the hub binds `<ctl>/tcp` (control) and every user port on
`ListenAddr` (user; chisel's server listens when the client's reverse remote
is accepted). The node binds nothing. The canary renders only the first port
map on `127.0.0.1`. IPv6 addresses are bracketed (`R:[::]:443:[::1]:9444`).

## Keys (`GenerateKeys`, pure Go)

| Key | Meaning |
| --- | --- |
| `server_key` | ECDSA P-256 private key, PEM `EC PRIVATE KEY` (what `chisel server --keygen` writes); becomes `server.key` on the hub |
| `fingerprint` | standard base64 of SHA-256 over the SSH wire-format public key (chisel `FingerprintKey`, 44 characters); the node pins it with `--fingerprint` |

`Validate` re-derives the fingerprint from `server_key` and refuses a mismatch.

## Credentials

User `dey`; the password is `hex(HMAC-SHA256(tunnel token, "deyroute/chisel/auth"))[:32]`
(`chisel.Password`). chisel v1.12.1 reads client credentials only from
`--auth` or the `AUTH` environment variable — there is no file option — so the
node passes them in `AUTH`. Environment values of a unit are visible to local
users through `systemctl show`; deriving the password keeps the tunnel token
(shared with the tunnel's other backends) out of the unit. The hub uses
`--authfile`, so nothing secret is in its argv.

## Security (spec section 11)

* The authfile grants exactly the tunnel's reverse remotes: no other port on
  the hub, no forward remotes (the hub never dials on behalf of a node), no
  SOCKS (`TestParsedAndAllowList`).
* The node pins the hub twice: TLS chain against the tunnel CA (+ name via
  `--sni` when set) and the SSH host key fingerprint.
* The node client dials the targets named in its own remotes. chisel's client
  does not filter the destinations its server asks for; only the hub (which
  already controls the node through the control channel) could ask for
  others.

## Differences between the spec sample and the pinned version

| Spec 7.9 sample | Rendered | Why |
| --- | --- | --- |
| `--auth user:pass` on the server | `--authfile users.json` with anchored `R:` patterns | restricts the user to the tunnel's ports; secret not in argv |
| `--auth user:pass` on the client | `AUTH` environment variable, derived password | chisel reads credentials only from `--auth`/`AUTH`; see Credentials |
| (none) | `--keyfile server.key` / `--fingerprint` | stable host key; otherwise chisel generates a new key per start and the client cannot pin it |
| `https://hub:<ctl>` | plus `--tls-ca <ca.crt>` and `--sni <server name>` | verify the tunnel certificate against the internal CA |
| `R:0.0.0.0:443:127.0.0.1:443` | `R:<ListenAddr>:<listen>:<target host>:<target port>[/udp]` | per port map; `/udp` for udp maps |
| (none) | `--keepalive 25s`, `--max-retry-interval 10s` | keepalive is chisel's default made explicit; the retry cap is lowered from 5 minutes so a restarted hub is reachable again quickly |

## Known limitations

* The server's TLS minimum version is chisel's (Go default); Go clients
  negotiate TLS 1.3.
* The node service sees the chisel client as the source (client IP masked).
* sha256 of the release files is empty until `scripts/manifest-hashes.sh`
  fills it (QUESTIONS.md C.9).
