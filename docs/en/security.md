# Security

[فارسی](../fa/security.md) · [Index](index.md)

The rule: everything that reaches the hub from outside is either user traffic
on a tunnel port or an mTLS connection from a known node. No management port
is open to the public, and no secret is stored without mode 0600 or written
to a log.

## Control channel (hub ↔ nodes)

- During setup the hub creates an **internal CA** (Ed25519). The hub
  certificate and every node certificate (valid 10 years) come from it.
- A node connects to the hub's control port (44433 by default) with **mutual
  TLS**, TLS 1.3 only. Without a certificate from this CA the hub refuses
  the request (`DEY-N013`).
- The node trusts only the CA whose fingerprint was in its join link
  (pinning); the system CA store is not used.
- The connection always goes from the node to the hub. The hub never logs
  in to a node, and SSH is not used.
- A node takes no decisions of its own: it only renders files below
  `/etc/deyroute/backends/`, runs deyroute's own backend binaries, and downloads
  only over https.

## Join tokens

- 32 random bytes, **single use**, valid 15 minutes by default
  (`deyroute node join-command --ttl` accepts 1m to 24h), deleted as soon as
  they are used.
- More than 5 failed attempts in an hour from one address block it for one
  hour (`DEY-N007`).
- While an unused token exists, the control port accepts connections from
  every address (the join window). Otherwise it accepts the joined nodes'
  IPs, plus a few new connections per minute from other addresses so that a
  node whose IP changed can reconnect with its certificate.
- `deyroute security audit` warns about every unexpired join token: each one
  keeps the join window open until it expires. `deyroute doctor` reports the
  tokens made with a `--ttl` over 15 minutes (with their expiry) and expired
  tokens that could not be removed.
- A join that is refused for another reason (the node id is taken, the
  request is broken, `config.yaml` has an edit that is not applied:
  `DEY-C026`) does not use up the token; the node can try again with the
  same command.

## Firewall: `table inet deyroute`

DEYROUTE creates and manages two nftables tables, `inet deyroute` (below) and,
on the hub, the traffic counting table `inet deyroute_stats` (next section),
and never touches other tables or rules. On a hub with two nodes and a tunnel
on 443 and 2053 `inet deyroute` looks like this (the join-window and IPv6 lines appear only when needed):

```text
table inet deyroute {
	set nodes {
		type ipv4_addr
		elements = { 1.2.3.4, 9.8.7.6 }
	}

	chain input {
		type filter hook input priority -10; policy accept;
		ct state established,related accept
		iif "lo" accept
		tcp dport 44433 ip saddr @nodes accept
		tcp dport 44433 ct state new limit rate 6/minute accept
		tcp dport 44433 drop
		tcp dport 30000-31999 ip saddr @nodes accept
		udp dport 30000-31999 ip saddr @nodes accept
		tcp dport 30000-31999 drop
		udp dport 30000-31999 drop
		tcp dport { 443, 2053 } accept
	}
}
```

- The control port and the backend control ports (30000–31999) are open
  only to the nodes; the tunnel ports are open to everyone.
- A node's own `inet deyroute` table holds only NAT rules of its transports.

```bash
deyroute security firewall show      # the table and the firewalls found (ufw, firewalld …)
deyroute security firewall apply     # render and apply it now
deyroute security firewall disable   # delete the table; deyroute only suggests commands then
```

With `security.firewall_managed: false` in `config.yaml` DEYROUTE applies
nothing and only prints the commands you should run (`DEY-P031`). A table
`inet deyroute` applied earlier is removed, also when the setting changed
while the hub was not running (an edit followed by a restart, a restore). An
external firewall (ufw, firewalld, provider panel) is never changed without
your confirmation; `deyroute port check` shows the exact command, and
`deyroute port check <port> --open` (or `Open it in the firewall` in the menu)
runs it only after you type `yes`. The hub never runs text it receives: it
checks the firewall again and builds the command from the firewall it finds
and the port number and protocol, and refuses (`DEY-P032`) when that differs
from the command you confirmed. This also works with
`security.firewall_managed: false`, which concerns only table `inet deyroute`.
A provider's firewall panel is never touched.

## Traffic counting: `table inet deyroute_stats`

To show how much each tunnel carries (`deyroute stats`, the dashboard,
`deyroute tunnel show`) the hub keeps a second, separate table. It only
counts: every chain has `policy accept` and there is no accept, drop,
reject, jump or NAT rule in it, so it can never block or change traffic. For
the tunnel above it is:

```text
table inet deyroute_stats {
	counter tun_main_in {
		packets 0 bytes 0
	}

	counter tun_main_out {
		packets 0 bytes 0
	}

	map acct_in {
		type inet_proto . inet_service : counter
		elements = { tcp . 443 : "tun_main_in", tcp . 2053 : "tun_main_in" }
	}

	map acct_out {
		type inet_proto . inet_service : counter
		elements = { tcp . 443 : "tun_main_out", tcp . 2053 : "tun_main_out" }
	}

	chain count_in {
		type filter hook input priority 300; policy accept;
		iif != "lo" counter name meta l4proto . th dport map @acct_in
	}

	chain count_out {
		type filter hook output priority 300; policy accept;
		oif != "lo" counter name meta l4proto . th sport map @acct_out
	}
}
```

- **Totals per tunnel, nothing else.** There is one pair of counters per
  tunnel (bytes and packets towards its listen ports and back). No client
  IP address, connection, destination or time of day of a user is recorded,
  in the kernel or in `state.db`. The hub stores the totals as a time
  series (1-minute points for a day, half-hour points for 32 days, one total
  per quota period) in `/var/lib/deyroute/state.db`.
- **No connection tracking.** The counters match the protocol and the
  listen port only, after the filter chains (priority 300), so a packet
  another firewall drops is not counted and loopback traffic (probes,
  diagnostics) is left out. Only a tunnel with a kernel NAT rung (WireGuard,
  AmneziaWG) adds a forward chain that uses conntrack, which that NAT
  already needs.
- **Directions are seen from the users:** "in" is upload from users to the
  hub, "out" is download to users. The bytes are layer-3 bytes on the
  user-facing ports. A provider usually bills the hub's network card, which
  also carries the tunnel to the node, so its count is about twice as high:
  a quota (`advanced.monthly_quota_gib`) counts user-side traffic only.
- The table is separate from `inet deyroute`, so firewall changes never
  reset the counters, and it works with `security.firewall_managed: false`.
  It is rebuilt only when the set of tunnel ports changes, with the last
  values as start values; after a reboot or `systemctl restart nftables`
  (`flush ruleset`) the hub rebuilds it within seconds from the last stored
  reading.
- `monitoring.enabled: false` in `config.yaml` removes the table and stops
  the counting; `deyroute uninstall` removes it too. Without nft or
  nf_tables (an unprivileged container) the hub reports `DEY-X061` and shows
  connection counts only, never an unknown volume as `0 B`.
- Tunnel volumes (today's bytes, the counters since their last reset) appear
  in `deyroute stats --json` and in doctor bundles (`deyroute doctor --out
  FILE`), never addresses of users.

```bash
nft list table inet deyroute_stats   # the counting table and its counters
deyroute stats                       # the totals per tunnel
```

## Secrets

- `/etc/deyroute/secrets/` is 0700 and every file in it 0600, owned by root:
  the CA key, hub/node keys, join tokens, one 32-byte token per tunnel, and
  the tunnel TLS keys. `config.yaml` is 0600 too.
- Backends run as user `deyroute` and get only the copies they need, below
  `/etc/deyroute/backends/` (0640 root:deyroute).
- Tokens, keys and passwords never appear in logs, error messages or doctor
  output; they are replaced by `***`. The doctor support file is scanned once
  more before it is written; if a secret is still found no file is written
  (`DEY-X060`).
- The menu and the CLI talk to the service through `/run/deyroute/daemon.sock`
  (0600): only root can use them.
- A secret file with wrong permissions is reported as `DEY-S002`; fix it with
  `chmod 600 <file> && chown root:root <file>`.

## Rotating tokens and the CA

```bash
deyroute security rotate-tokens                      # every tunnel
deyroute security rotate-tokens --tunnel main --yes  # one tunnel
deyroute security rotate-ca                          # Advanced
```

- `rotate-tokens` replaces the secret token of the tunnel(s). The old token
  stops working at once; every transport of the tunnel restarts with the new
  token on the hub and the nodes (a short interruption). It asks you to type
  `yes`.
- `rotate-ca` replaces the internal CA and the hub certificate and re-issues
  the certificate of every **online** node. Offline nodes can no longer
  connect and must join again; join commands created before stop working.

Menu: `8) Security` → `1) Rotate tokens`.

## Tunnel TLS certificates

Some transports wrap the tunnel in TLS (for example `backhaul/wssmux`,
`frp/*`). This is the tunnel's own certificate, not your service's; your
users' TLS passes through untouched.

| Mode (`tunnels[].tls.mode`) | What happens |
| --- | --- |
| `auto` (default) | a certificate per tunnel from the internal CA, 3 years, renewed automatically 30 days before expiry; the node checks it against the pinned CA |
| `acme` | Let's Encrypt for `hub.domain` (DNS-only, no Cloudflare proxy; HTTP-01 on port 80, or DNS-01 with a Cloudflare token). If it fails, the tunnel falls back to `auto` (event `acme_failed`, `DEY-T003`) and keeps running |
| `custom` | your own certificate and key files, validated before use |

```bash
deyroute security tls show              # expiry and fingerprint of every certificate
deyroute security tls show --tunnel main
deyroute security tls renew --tunnel main
```

The dashboard warns 14 days before a certificate expires. The TLS mode is set
per tunnel in Advanced mode (`2) Tunnels` → `2) Edit tunnel`) or with
`deyroute tunnel edit main --tls-mode auto|acme|custom` (`custom` also takes
`--tls-cert` and `--tls-key`).

### A Let's Encrypt certificate (`acme`)

1. Point a domain at the hub: an `A` record (and `AAAA` if the hub has
   IPv6) with **DNS only** — no Cloudflare proxy (orange cloud).
2. Set it as the hub domain. Every tunnel certificate then also names it.

   ```bash
   deyroute security tls domain vpn.example.com
   deyroute security tls domain --clear       # remove it again
   ```

   Menu: `8) Security` → `2) TLS certificates` → `2) Domain (for ACME)`
   (type `-` to remove it). The domain cannot be removed while a tunnel
   uses `acme`.
3. Switch the tunnel: `deyroute tunnel edit main --tls-mode acme` (menu:
   Advanced, `2) Tunnels` → `2) Edit tunnel`).
4. Request the certificate now with `deyroute security tls renew --tunnel
   main` (menu: `8) Security` → `3) Renew TLS certificate`), or let the
   daily renewal do it.

Let's Encrypt checks the domain with **HTTP-01 on port 80** of the hub.
When port 80 is taken, use **DNS-01 through Cloudflare** instead (Advanced):
create a Cloudflare API token with `Zone:DNS:Edit` for the domain's zone,
put it in a file and give deyroute the file:

```bash
deyroute security tls acme --cloudflare-token-file /root/cloudflare.token
deyroute security tls acme --email owner@example.com      # optional: expiry notices
deyroute security tls acme --cloudflare-token-file ''      # back to HTTP-01
```

Menu (Advanced): `8) Security` → `2) TLS certificates` → `3) ACME e-mail`
and `4) Cloudflare token (DNS-01)`. The token itself is never typed into the
menu or a command line: deyroute copies it from your file to
`/etc/deyroute/secrets/cloudflare.token` (mode 0600), so you may delete your
file afterwards. `config.yaml` only names that file
(`hub.acme.cloudflare_token_file`) and the token never appears in logs. The
`TLS certificates` screen shows the domain and which check is used.

If Let's Encrypt fails (`DEY-T003`, `DEY-T004`), the tunnel keeps running
with its internal certificate (`acme_failed` event) and the daily renewal
tries again.

## Audit

```bash
deyroute security audit
```

Lists publicly open ports, file permissions, certificate expiry, nodes with
an old version and unexpired join tokens. Run it after every
security change. Menu: `8) Security` → `5) Audit`.

## Tunnel processes

- Each transport runs as its own systemd unit, as user `deyroute`, with only
  the right to bind low ports (`CAP_NET_BIND_SERVICE`) and a hardened sandbox
  (`NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, a
  system-call filter …). Only WireGuard transports run as root with
  `CAP_NET_ADMIN`.
- A crash of one backend never takes the hub down; systemd restarts it.

## A node is never an open proxy

Transports where the hub connects to the node (`xray/reality`,
`hysteria2/udp`, `direct/*` …) allow connections only to `127.0.0.0/8` and to
the targets defined in the tunnel; everything else is blocked (Xray routing,
the relay's allow list, the Hysteria2 ACL). WireGuard transports only deliver
tunnel traffic to the targets, and the firewall drops anything the node would
route onward from the tunnel interface, even when IP forwarding is on (for
example on a Docker host). Nobody can use your node to reach other addresses
through the tunnel; the lab scenario S17 checks this for every Forward
transport from inside the transport itself.

## Downloads and no telemetry

- Releases are signed: `SHA256SUMS` with minisign; the installer and deyroute
  itself check the signature and the checksum.
- Backend binaries are installed only when their sha256 matches the signed
  manifest (`DEY-S001`, `DEY-S006`). No third-party script is ever piped into
  a shell.
- **No telemetry.** DEYROUTE sends no data about you or your users anywhere.
  The only outside traffic is: downloading DEYROUTE releases and backend
  binaries from GitHub or your mirror (through a node when the hub cannot
  reach GitHub), and Telegram messages if you turn notifications on (the hub
  sends them itself, or through a node when api.telegram.org is blocked).

## Checklist

- [ ] Apart from SSH, the hub exposes only the tunnel ports to the internet (the control ports only to your nodes).
- [ ] `deyroute security audit` is clean.
- [ ] A recent, encrypted backup is stored outside the server.
- [ ] Nodes run the same version as the hub (`deyroute node list`).
- [ ] Time sync is on (`timedatectl set-ntp true`) on every server.
