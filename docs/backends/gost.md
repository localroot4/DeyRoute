# Gost backend (`gost/relay-wss`, optional)

| | |
| --- | --- |
| Upstream | [go-gost/gost](https://github.com/go-gost/gost) (v3) |
| Pinned version | **v3.2.6** (`internal/backend/backends.yaml`); its `go.mod` pins `github.com/go-gost/x v0.8.1` and `github.com/go-gost/core v0.3.3`, which hold the config schema and every handler/listener |
| Reference | [README at v3.2.6](https://github.com/go-gost/gost/blob/v3.2.6/README.md); `cmd/gost/{main,program}.go` at the tag (`-C` config file); x v0.8.1 `config/config.go` (schema), `config/cmd/cmd.go` (how `-L`/`-F` URLs become services/chains), `config/parsing/{service,node,bypass}/parse.go`, `handler/relay/{handler,bind,connect,metadata}.go`, `handler/forward/remote`, `listener/{ws,rtcp,rudp}`, `dialer/ws`, `connector/relay`, `internal/util/tls/tls.go`, `bypass/bypass.go` |
| Release assets | `gost_3.2.6_linux_amd64.tar.gz`, `gost_3.2.6_linux_arm64.tar.gz` (goreleaser default name template, `.goreleaser.yaml` at the tag), binary `gost`, upstream checksums in `checksums.txt`. The linux amd64/arm64/arm binaries are **UPX-compressed** (`upx:` section). |
| Package | `internal/backend/gost` |
| Direction | Reverse: the hub runs the relay server, the node runs remote port forwards |

## Transport

| Transport id | Carries | Needs UDP between hub and node | Needs tunnel TLS | Stealth | Optional |
| --- | --- | --- | --- | --- | --- |
| `gost/relay-wss` | tcp, udp | no | yes | 3 | yes (section 7.9: only when the owner adds it) |

On the wire: one TLS 1.3 session to `<hub>:<ctl>` carrying a WebSocket to a
per-tunnel path (`/` + 16 hex chars derived from the token, instead of gost's
well-known `/ws`), with gost's relay protocol and a mux inside. UDP port maps
travel through the same connection (`rudp` over relay BIND).

## How each side is rendered

Both sides run `gost -C <ConfigDir>/gost.yaml` (config file instead of `-L`/`-F`
URLs so the token is not visible in `ps`). The file is YAML in the v3 schema
(gost reads it through viper; keys are matched case-insensitively).
`MemoryDenyWriteExecute` is dropped for this unit with the documented reason
that the release binaries are UPX-packed (the UPX loader makes its unpacked
code executable at start). Files live in
`/etc/deyroute/backends/gost/<tunnel>/<node>/relay-wss/` (`0640 root:deyroute`).

Hub (shortened):

```yaml
services:
  - name: relay-wss
    addr: 0.0.0.0:30001
    bypass: deny-connect          # TCP-only tunnel: relay CONNECT refused (tunnels with udp maps: deny-local, see Security)
    handler:
      type: relay
      auth: {username: dey, password: <token>}
      metadata: {bind: true}
    listener:
      type: wss
      tls: {certFile: …/tls-cert.pem, keyFile: …/tls-key.pem, options: {minVersion: VersionTLS13}}
      metadata: {path: /b7c36e39cff03bb4}
bypasses:
  - name: deny-connect
    whitelist: true               # empty whitelist = every address bypassed
log: {output: stderr, level: info, format: text}
```

Node (one service per port map; `rudp` for udp maps):

```yaml
services:
  - name: tcp-443
    addr: 0.0.0.0:443             # bound on the HUB through the chain
    handler: {type: rtcp}
    listener: {type: rtcp, chain: hub}
    forwarder: {nodes: [{name: target, addr: 127.0.0.1:443}]}
  - name: udp-27015
    addr: 0.0.0.0:27015
    handler: {type: rudp}
    listener: {type: rudp, chain: hub, metadata: {ttl: 60s}}
    forwarder: {nodes: [{name: target, addr: 10.0.0.5:27016}]}
chains:
  - name: hub
    hops:
      - name: hop-0
        nodes:
          - name: hub
            addr: 5.6.7.8:30001
            connector: {type: relay, auth: {username: dey, password: <token>}}
            dialer:
              type: wss
              tls: {caFile: …/ca.crt, secure: true, serverName: 5.6.7.8, options: {minVersion: VersionTLS13}}
              metadata: {path: /b7c36e39cff03bb4}
```

Binds: the hub binds `<ctl>/tcp` (purpose `control`) and every user port on
`ListenAddr` (purpose `user`; gost binds them when the node's `rtcp`/`rudp`
listeners ask through relay BIND). The node binds nothing. The canary renders
only the first port map and always binds `127.0.0.1`.

TLS: the hub serves the tunnel certificate; the node verifies the chain
against the tunnel CA (`caFile`) and, when the planner provides a server name,
the name as well (`secure: true` + `serverName`). Without a server name gost
v3 still verifies the chain against `caFile`
(`internal/util/tls.LoadClientConfig` installs a `VerifyConnection` with the
CA roots when `secure` is false).

## Security (spec section 11)

* The node runs only remote-forward handlers whose forwarder is the one
  configured target: it never dials anything else and exposes no proxy
  (`TestNodeDialsOnlyTargets`).
* TCP-only tunnels: the hub's relay refuses every CONNECT (bypass
  `deny-connect`, a whitelist without entries), so a holder of the token
  cannot use the hub as a proxy.
* Tunnels with udp port maps: gost v3 applies the service bypass **also to
  every datagram of a UDP BIND**, matched against the user client's address
  (x v0.8.1 `handler/relay/bind.go` → `internal/net/udp/relay.go`), so the
  deny-all whitelist would silently drop all UDP traffic. These tunnels get
  bypass `deny-local` instead, a blacklist of `0.0.0.0/8`, `169.254.0.0/16`,
  `::/128` and `fe80::/10`: CONNECT to unspecified and link-local addresses
  (e.g. the cloud metadata service) is refused, ordinary client addresses
  pass. Loopback is not listed because the hub's own UDP path probe and the
  canary are loopback clients; CONNECT to other destinations is possible
  for a holder of the tunnel token (only the tunnel's nodes, and the control
  port accepts only node IPs, `@nodes`). `TestHubUDPNotBypassed` checks both
  cases with gost's matching rules.
* relay BIND itself has no address filter in gost v3; it requires the tunnel
  token and the control port is reachable only from node IPs.
* No API, metrics or profiling service is configured.

## Differences between the spec sample and the pinned version

| Spec 7.9 sample | Rendered | Why |
| --- | --- | --- |
| `gost -L relay+wss://:<ctl>?bind=true` (+ TLS) | `services[0]` with `handler.type: relay`, `handler.metadata.bind: true`, `listener.type: wss`, `listener.tls.certFile/keyFile` | same service in the config-file form (`config/cmd/cmd.go` maps the URL to exactly these keys); `?certFile=`/`?keyFile=` query keys become `listener.tls` |
| no auth on the hub | `handler.auth: {username: dey, password: <token>}` | the relay must only accept the tunnel's nodes |
| `-L rtcp://:443/127.0.0.1:443` | service `addr: <ListenAddr>:443`, `handler`/`listener` type `rtcp`, `listener.chain`, `forwarder.nodes[0].addr: <target>` | config form of the URL (`cmd.go`: rtcp/rudp services put the chain on the listener) |
| `-F relay+wss://user:pass@hub:<ctl>` | `chains[0].hops[0].nodes[0]` with `connector.type: relay` + `auth`, `dialer.type: wss` + `tls` | config form of `-F`; user `dey`, password = tunnel token |
| (none) | `dialer.tls.caFile/secure/serverName`, `options.minVersion: VersionTLS13` | node pins the tunnel CA; TLS 1.3 only (section 10) |
| (none) | `metadata.path` on listener and dialer | per-tunnel WebSocket path instead of the default `/ws` |
| (none) | `rudp` listener `metadata.ttl: 60s` | gost's 5 s default idle timeout drops paused UDP sessions |
| (none) | `bypass: deny-connect` (tcp-only) / `deny-local` (with udp maps) | CONNECT is not needed; see Security |

## Known limitations

* In tunnels with udp port maps, relay CONNECT is only restricted for
  unspecified and link-local destinations (see Security).
* The service on the node sees the node-local gost process as the client
  (client IP masked).
* Stealth depends on TLS: the per-tunnel path hides the relay endpoint from
  probes that do not know the token, but the WebSocket upgrade and mux traffic
  pattern are gost's.
* sha256 of the release archives is empty until `scripts/manifest-hashes.sh`
  fills it (QUESTIONS.md C.9); the installer refuses the backend until then.
