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
  `/etc/deyroute/backends/`, runs only the backend binaries deyroute installed
  (and deyroute's own relay and WireGuard setup) with no more privileges than
  its built-in allowlist grants that backend (see Tunnel processes below),
  installs a new deyroute only from a release signed with the key built into
  it, and a backend only in the version a signed manifest pins (see
  Downloads below), and downloads only over https.

## Join tokens

- 32 random bytes, **single use**, valid 15 minutes by default
  (`deyroute node join-command --ttl` accepts 1m to 24h), deleted as soon as
  they are used.
- More than 5 failed attempts in an hour from one address block it for one
  hour (`DEY-N007`).
- While an unused token exists, the control port accepts connections from
  every address (the join window). Otherwise it accepts the joined nodes'
  IPs, plus 3 new connections per minute from each other address (30 per
  minute from all of them together) so that a node whose IP changed can
  reconnect with its certificate (see Unknown sources below).
- `deyroute node join-revoke` deletes every unused token and applies the
  firewall at once, so the join window closes without waiting for the expiry
  (event `join_tokens_revoked`). `deyroute setup --no-join` sets a hub up
  without a join command, so no join window opens at setup (see
  [Join a node](join.md#revoke-unused-join-commands)).
- Releases before 0.3.1 do not know the event name `join_tokens_revoked`:
  while `hub.notify.telegram.events` names it, `deyroute update --rollback`
  to such a release is refused (`DEY-S011`).
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

	set ctl_unknown4 {
		type ipv4_addr
		size 65535
		flags dynamic,timeout
		timeout 10m
	}

	set ctl_unknown6 {
		type ipv6_addr
		size 65535
		flags dynamic,timeout
		timeout 10m
	}

	chain input {
		type filter hook input priority -10; policy accept;
		ct state established,related accept
		iif "lo" accept
		tcp dport 44433 ip saddr @nodes accept
		tcp dport 44433 ct state new add @ctl_unknown4 { ip saddr limit rate 3/minute burst 3 packets } limit rate 30/minute accept
		tcp dport 44433 ct state new add @ctl_unknown6 { ip6 saddr limit rate 3/minute burst 3 packets } limit rate 30/minute accept
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

### Unknown sources on the control port

A node whose public IP changed is not in `@nodes` yet: it reconnects with
its certificate through the admission for unknown sources, and the hub then
puts its new address into `@nodes`. Each source address has its own budget of
3 new connections per minute (the dynamic sets `@ctl_unknown4` and
`@ctl_unknown6` keep one bucket per address for 10 minutes), and all unknown
sources together get at most 30 per minute. A scanner therefore uses up only
its own budget, not that of the node. mTLS and the join limiter still guard
every connection that gets through.

```yaml
security:
  unknown_control_rate_per_source: 3/minute   # per address; default 3/minute
  unknown_control_rate: 30/minute             # all unknown sources; default 30/minute
```

A rate is an nft rate: a number per `second`, `minute`, `hour` or `day`,
optionally with `burst N packets`. Without a burst, the per-source rate gets
a burst of its own number (`3/minute` allows 3 at once). Both keys are
absent by default. Releases before 0.3.1 do not know them:
`deyroute update --rollback` to such a release is refused (`DEY-S011`)
while one is set.

The per-source form needs nftables support for dynamic sets with a limit
per element. Every distribution the lab tests ships it (nftables 1.0.2 on
Ubuntu 22.04, 1.0.6 on Debian 12, 1.0.9 on Ubuntu 24.04, 1.1.x on Debian 13
and Ubuntu 26.04). When nft still refuses it (a syntax error, or a kernel
without that support), the hub applies the earlier single rule
`tcp dport 44433 ct state new limit rate 30/minute accept` at
`unknown_control_rate` instead, keeps that until it restarts and writes the
warning `nft refused the per-source limit of the control port` once to
`hub.log`.

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

### The table comes back by itself

Another tool can remove or change `inet deyroute`: `nft flush ruleset` by
an admin or a provider script, `ufw reload` on some systems. The hub and
every node therefore list the table every 60 seconds and compare it with the
one they applied. A missing or changed table is applied again at once, under
the same lock as every other apply, so a check never races a real change. A
node checks only while it has NAT rules (a WireGuard or AmneziaWG rung,
Hysteria2 port hopping); without them it has no table. The order in which
nft lists set elements does not count as a change, nor do the addresses
the kernel adds to a dynamic set.

```yaml
security:
  firewall_reassert_s: 60   # seconds, 10-3600; 0 = off; default 60
```

The key is absent by default. With `security.firewall_managed: false`
nothing is ever checked or applied. The hub records the event
`firewall_reapplied` (reason `missing` or `drift`, see
[Traffic and load](monitoring.md#firewall-events)); a node writes the same
warning to its log (`/var/log/deyroute/node.log`). Each reason is reported at
most once per 10 minutes; the table is applied again every time. Releases
before 0.3.1 do not know the key: `deyroute update --rollback` to such a
release is refused (`DEY-S011`) while it is set. Lab scenario S37 flushes
the ruleset on a hub and on a node with a WireGuard rung and checks that the
table comes back while the tunnel stays up.

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
an old version, unexpired join tokens and Telegram notifications. Run it
after every security change. Menu: `8) Security` → `5) Audit`.

## Tunnel processes

- Each transport runs as its own systemd unit, as user `deyroute`, with only
  the right to bind low ports (`CAP_NET_BIND_SERVICE`) and a hardened sandbox
  (`NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, a
  system-call filter …).
- A backend gets more only where the allowlist below says so. The list is
  built into deyroute, so the hub cannot change it: a node refuses a unit that
  asks for anything else (`DEY-N050`, naming the field), and the hub checks its
  own units against the same list before it writes them (`DEY-X034`).
- No backend may relax `NoNewPrivileges` or the capability bounding set.
- A crash of one backend never takes the hub down; systemd restarts it.

| Backend (transport) | Runs as root | Extra capability | Extra address family | Hardening it may relax | Extra writable path |
| --- | --- | --- | --- | --- | --- |
| `wireguard` (`wireguard/kernel`) | yes | `CAP_NET_ADMIN` | `AF_NETLINK` | `ProtectKernelTunables` (sets `route_localnet` when the node forwards to `127.0.0.1`) | none |
| `awg` (`awg/userspace`) | yes | `CAP_NET_ADMIN` | `AF_NETLINK` | none | `/run/amneziawg` |
| `rathole` | no | none | none | `MemoryDenyWriteExecute` (the release binaries are UPX-packed) | none |
| `direct/haproxy` (the node's address check) | no | none | `AF_NETLINK` | none | none |
| every other backend and transport | no | none | none | none | none |

## The hub and node services

`deyroute-hub` and `deyroute-node` run as root: they manage nftables,
systemd units and kernel settings. Their units still keep them in a sandbox,
so a bug in the daemon cannot write anywhere it likes:

- `ProtectSystem=strict`: the whole file system is read-only for the daemon,
  except the paths below. `ProtectHome`, `PrivateTmp` and `NoNewPrivileges`
  stay as before.
- A capability bounding set: the daemon keeps only the root rights it uses.
  `CAP_SYS_ADMIN`, `CAP_SYS_MODULE`, `CAP_SETUID`, `CAP_SETGID` and
  `CAP_KILL` are gone.
- `ProtectControlGroups`, `RestrictSUIDSGID`, `LockPersonality` and
  `SystemCallArchitectures=native`.

| Writable path | Hub | Node | Why |
| --- | --- | --- | --- |
| `/etc/deyroute` | yes | yes (all of `/etc`) | configuration, secrets, backend configs, manifest |
| `/etc/systemd/system` | yes | yes (all of `/etc`) | tunnel units, their drop-ins, the tuning's resource drop-ins and slice |
| `/etc/sysctl.d`, `/etc/modules-load.d`, `/etc/modprobe.d` | yes, if present | yes (all of `/etc`) | the files of the kernel tuning |
| `/var/lib/deyroute` | yes | yes (all of `/var/lib`) | state, backend binaries, `deyroute.prev`, backups, downloads |
| `/var/log/deyroute` | yes | yes (all of `/var/log`) | daemon and tunnel logs |
| `/usr/local/bin` | yes | yes | `update` and `update --rollback` replace `deyroute` and `dey` |
| `/run` | yes | yes | the local API socket, the iptables lock of the firewall check |

The node needs all of `/etc`, `/var/lib` and `/var/log`: the uninstall
that the hub sends runs inside the node service. It deletes the
directories `/etc/deyroute`, `/var/lib/deyroute` and `/var/log/deyroute`
themselves and removes the `deyroute` account with `userdel` and
`groupdel`, which rewrite `/etc/passwd` and `/etc/group` through
temporary files in `/etc`. `/usr`, `/boot` and the rest of the system stay
read-only on both.

| Capability | Used for |
| --- | --- |
| `CAP_NET_ADMIN` | nftables, the `net.*` kernel settings, `ip` and the netlink queries |
| `CAP_NET_RAW` | `iptables -S` of the firewall check (iptables-legacy opens a raw socket) |
| `CAP_NET_BIND_SERVICE` | listeners below port 1024: port checks, the hub's CDN front, ACME HTTP-01 |
| `CAP_DAC_OVERRIDE` | files of the `deyroute` user: tunnel logs, backend files |
| `CAP_CHOWN`, `CAP_FOWNER` | the group `deyroute` and the modes of backend configs, secrets and log directories |
| `CAP_SYS_PTRACE` | finding the program that holds a port (`ss -p`, `/proc/<pid>/fd`) and the virtualization check |
| `CAP_AUDIT_WRITE` (node only) | `userdel` and `groupdel` of the uninstall write audit records |

Kernel settings need no extra capability: root may write `/proc/sys` and
the conntrack hash size. The kernel loads `nf_conntrack`, `tcp_bbr` and
`sch_fq` by itself when the tuning uses them; the daemon never loads a
module. `ProtectKernelModules` stays off only so the host check can read
`/lib/modules` to see whether BBR is available.

Check the sandbox on a server:

```bash
systemctl show deyroute-hub -p ProtectSystem -p CapabilityBoundingSet
systemd-analyze security deyroute-hub
```

The exposure score that `systemd-analyze security` prints falls from 8.9
to 6.5 (hub) and 6.6 (node). The new units come with the installer: a new
install, a join and running `install.sh` again on a server write them.
`deyroute update` alone replaces only the binary and keeps the units it
finds. A server where `/etc/sysctl.d`, `/etc/modules-load.d` or
`/etc/modprobe.d` did not exist when the hub started cannot get the tuning
files there: create the directory and restart the service.

## Tunnel logs

- The transports of a tunnel write to `/var/log/deyroute/tunnels/<tunnel>.log`.
  systemd opens that file as root before it starts the backend, which only
  writes through the file it was given. The directory is `1770
  root:deyroute`: the `deyroute` user may add files there, but the sticky
  bit stops it from removing or renaming root's files (the live logs and
  the rotated `.gz` files). The installer, `deyroute setup` and the
  services create it so. On a server installed before, the hub and node
  services add the sticky bit to the existing directory when they start,
  so `deyroute update` (which restarts them) is enough; running the
  installer again (it repairs and keeps the configuration) also sets the
  mode. The services only add the sticky bit, and never to a directory
  root does not own: a link at that name or a directory of another user is
  left as it is and logged as the warning
  `cannot make the tunnel log directory sticky`, and no transport starts
  there (next point). Check it with
  `stat -c '%a %U:%G' /var/log/deyroute/tunnels` (`1770 root:deyroute`).
- Before a transport starts, and for every tunnel when the service starts,
  the hub and the node make sure that `<tunnel>.log` is root's regular file
  with a single link: systemd would follow a link at that name as root, or
  create the file it points to. Anything else found there (a link, a FIFO,
  a directory, a second hard link, a file of another user) is removed and
  an empty log is created (`0644`, as systemd creates it). A directory is
  not deleted on the spot: it is renamed
  `<tunnel>.log.removed-<16 hex digits>` next to it, which frees the name
  at once however deep it is, and the rotation deletes it in the
  background (below). The check when the service starts also covers a
  link put there before the directory was sticky. It needs `tunnels/` to
  be root's own directory with the sticky bit (or closed to its group and
  others): at a link, in a directory of another user, or in a
  group-writable one without the sticky bit, the transport is not
  started, nothing at the log name is touched and the hub or node answers
  `DEY-X022`, whose `Why` names the reason (fix:
  `chown root:deyroute /var/log/deyroute/tunnels && chmod 1770 /var/log/deyroute/tunnels`).
  Each replaced log is logged once per file and reason while the service
  runs, as a warning in `hub.log` or `node.log`:

  ```bash
  grep 'tunnel log replaced' /var/log/deyroute/node.log
  ```

  The line carries `file` and `reason`: `symlink`, `not_regular`,
  `hardlink` or `owner` (a file of another user).
- To free space, empty a live tunnel log; never delete it:

  ```bash
  : > /var/log/deyroute/tunnels/<tunnel>.log
  ```

  systemd restarts a transport that stops by itself (after 2 seconds) and
  opens its log again as root, without the hub or the node. While the name
  is free, the `deyroute` user can put a link there, and systemd would
  follow it. If a log was deleted, restart the service
  (`systemctl restart deyroute-hub` or `deyroute-node`): it creates the log
  again as root's file.
- `deyroute logs` and the log lines shown with a failed start (`DEY-B003`)
  read only a regular file with a single link: they never follow a link,
  never wait on a FIFO and never read a file with a second hard link at a
  log name (a node answers `DEY-X022`), as the rotation and
  `deyroute doctor` do.
- The hub and the node rotate these logs as root (above 20 MB, checked
  every minute, 5 compressed files kept). The rotation never follows a
  symbolic link and never writes through a file name the `deyroute` user
  could create first: a `*.log` that is a link, not a regular file, or has
  a second hard link is skipped; the copy is made in the root-only
  `/var/lib/deyroute/tmp` (0700); and anything the rotation finds at one of
  the names it writes in the tunnel log directory is removed, so the
  rotation goes on (a directory is renamed
  `<name>.removed-<16 hex digits>`). Every minute the rotation also
  deletes what was renamed so, in the tunnel log directory and in
  `/var/lib/deyroute/tmp`: at most 10000 entries per directory and minute,
  a link inside removed and never followed, so a tree as deep as the
  backend made it never holds up the service.
- Each such case is logged once per file and reason while the service
  runs, as a warning in `hub.log` or `node.log`:

  ```bash
  grep 'tunnel log rotation refused' /var/log/deyroute/node.log
  ```

  The line carries `file` and `reason`: `symlink`, `not_regular`,
  `hardlink` or `staging` (something stood at a name the rotation writes,
  or `/var/lib/deyroute/tmp` is not root-only). DEYROUTE never creates such
  files itself, for this warning or for `tunnel log replaced`: a process of
  the `deyroute` user, that is a tunnel backend, did. Check that server
  (`ps -u deyroute -o pid,args`), and update or replace the backend. The
  lab scenario S36 plants such links, also at the log name of a tunnel
  before it is added and at the log of a running tunnel before the
  services restart (then kills its backend, which systemd restarts by
  itself), a chain of 3000 directories at the log name of a new tunnel and
  a hard link at a log name, and checks that root files stay unchanged,
  that normal rotation goes on, that the tunnels come up, that the chain
  is deleted in the background and that `deyroute logs` refuses the hard
  link.

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
- **A node trusts the release key, not the hub.** When a node follows the
  hub's version, the hub passes on the signed files of its release
  (`SHA256SUMS`, `SHA256SUMS.minisig`, the archive), and the node checks
  them itself with the release key built into its own binary: the
  signature, that the release is exactly the announced version, the
  checksum of the archive, and that the archive holds one regular file
  `deyroute` and nothing that would land outside its directory. Last, it
  asks that binary whether it checks signed updates itself
  (`deyroute --json version` must say `"signed_update": true`): a release
  built before signed updates is signed with the same key, but a node on it
  would take any binary its hub serves. Anything else is refused with
  `DEY-S001` and nothing is installed. So someone who takes over the hub
  cannot make the nodes install a program of their own through an update,
  neither directly nor by way of an older release. What remains: they can
  still move the nodes to an older signed release that checks signed
  updates, and pin an older backend version that the signed manifest of an
  older release names (below). A hub on a development build has no signed
  release (`DEY-S012`); details in
  [Update & uninstall](update-uninstall.md#how-the-nodes-follow). The lab
  scenario S41 replaces the binary inside the release a hub keeps for its
  nodes and checks that the node refuses it and keeps its version; S19
  checks that a node follows a hub's `deyroute update` this way.
- `node.allow_unsigned_updates: true` in a node's `config.yaml` turns this
  check off for that node (test labs only); never set it on a server with
  real users. It does not apply to the backends below.
- Release binaries trust the release key only. Builds for the integration
  lab may trust a second, throwaway key; `deyroute --json version` shows
  `"test_key": true` for such a build and `false` for every release. The
  release builds check this for every binary and publish nothing when a
  binary says `true`.
- Backend binaries are installed only when their sha256 matches the signed
  manifest (`DEY-S001`, `DEY-S006`). No third-party script is ever piped into
  a shell.
- **The same holds for the backends on a node.** The hub tells a node which
  backend version to install, with its download address and sha256. The
  node installs it only when exactly this version, address and sha256 (for
  its architecture) are in the manifest built into its own binary, or in a
  manifest the hub sends along whose signature the node checks with the
  release key built into it: the hub's signed override
  (`deyroute update manifest`), or the signed manifest of the hub's
  release when the node runs another build. Anything else is refused with
  `DEY-N050` before a download. So someone who takes over the hub cannot
  make the nodes download a program of their own as a backend either;
  details in [Update & uninstall](update-uninstall.md#backends-on-the-nodes).
  The node checks the signature, not the age of the manifest, so a hub can
  still send the signed manifest of an older release and pin an older
  backend version from it.
- **No telemetry.** DEYROUTE sends no data about you or your users anywhere.
  The only outside traffic is: downloading DEYROUTE releases and backend
  binaries from GitHub or your mirror (through a node when the hub cannot
  reach GitHub), and Telegram messages if you turn notifications on
  (through an online node first, since api.telegram.org is often blocked
  from Iran, and directly from the hub when no node is online or the nodes
  fail).
- **The Telegram bot token goes to your nodes.** It is part of the Telegram
  API address, so the hub sends it to a node with every message, inside the
  node's control connection (mutual TLS). The node uses it only for that
  request to `api.telegram.org`, the only address it posts to for the hub,
  and keeps it out of its logs. Use a bot made for these alerts only.
  While notifications are on, `deyroute security audit` warns about it
  (check `telegram`), so the audit is never clean with Telegram on. If a
  node is lost or taken over, revoke the token in @BotFather and set the
  new one with `deyroute notify telegram set`.

## Checklist

- [ ] Apart from SSH, the hub exposes only the tunnel ports to the internet (the control ports only to your nodes).
- [ ] `deyroute security audit` is clean.
- [ ] A recent, encrypted backup is stored outside the server.
- [ ] Nodes run the same version as the hub (`deyroute node list`).
- [ ] Time sync is on (`timedatectl set-ntp true`) on every server.
