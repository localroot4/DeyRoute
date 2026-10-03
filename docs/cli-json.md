# `deyroute --json` output (schema 1)

Every `deyroute` command accepts the global `--json` flag (spec section 14). With
it, standard output carries exactly **one JSON document** (streams: one
document per line, see `status --watch` and `logs`), and everything meant for a
person — questions, confirmation texts, hints — goes to standard error.

This file is the stable contract for scripts. Field names never change within
schema 1; new fields may be added. Types refer to the Local API DTOs in
`internal/api/local.go` (the JSON names are listed here).

## Common rules

- Every document is an object with `"schema": 1`.
- Times are RFC 3339 strings in UTC (`"2026-09-30T12:00:00Z"`); durations in
  the Local API DTOs are integers in nanoseconds (Go `time.Duration`), unless
  the field name ends in `_s` (seconds) or `_ms` (milliseconds).
- The lists a command adds itself (`nodes`, `tunnels`, `steps`, `events`, …)
  are never `null`; an empty list is `[]`. Inside the Local API DTOs an empty
  list may be `null` or absent: treat it like `[]`.
- Exit codes are the same as without `--json`: `0` success, `1` user error
  (`DEY-C/P/T/N/B/F/S/I`, wrong arguments, a declined confirmation), `2`
  system error (`DEY-X`), `3` a destructive command needs `--yes` because
  stdin is not a terminal.
- Commands that report progress (tunnel add, delete, update, …) include the
  finished steps in `steps` (see **Step**). Running steps are not included.
- Units: byte counts are bytes (`bytes_*`, `today_*`, `*_bytes`), rates are
  bits per second (`rate_*_bit_s`), widths and durations ending in `_s` are
  seconds. Traffic directions are seen from the users: `in` = upload from
  users to the hub, `out` = download from the hub to users.

### Error document

When a command fails, standard output holds the error document (the human
three-line block is still printed on standard error):

```json
{
  "schema": 1,
  "exit_code": 1,
  "error": {"code": "DEY-P012", "message": "Port 443/tcp is already in use",
            "why": "nginx (pid 1234) is listening on 0.0.0.0:443",
            "fix": "choose another port, or stop nginx: systemctl stop nginx",
            "log": "/var/log/deyroute/hub.log", "detail": ""},
  "errors": [ … ]
}
```

- `error` is the first (or only) error; `errors` is present only when there
  are several (e.g. `config validate` reports every problem).
- A wrong command line (unknown command or flag, a missing flag value,
  the wrong number of arguments) is `DEY-C025` (exit 1); its `fix` names the
  `--help` of the command.
- A missing confirmation (exit 3) has `"code": ""` and the message
  `"This action needs confirmation; re-run with --yes"`.
- A declined confirmation (exit 1) has `"code": ""` and `"Aborted."`.

### Step

`{"id": "install_hub", "title": "install backend on hub", "status": "ok",
"detail": "…", "error": ErrorDTO?}` — `status` is one of `ok`, `failed`,
`skipped`, `warn`.

### Result of an action without data

`{"schema": 1, "ok": true, …}` plus the identifying fields listed per command.

## General

### `deyroute version`

`{"schema", "version", "commit", "date", "go"}` — `version` without a leading
`v` (`"dev"` for local builds).

### `deyroute setup`

Hub:
`{"schema", "role": "hub", "name", "public_ip", "public_ip6", "private_ip"
(bool: the detected address is not public), "control_port", "ca_fingerprint",
"config_path", "sysctl_profile", "sysctl_warnings": [string],
"firewall_managed", "service_started", "steps": [Step], "join": JoinCommand}`
— when the join command could not be obtained, `join_error` (ErrorDTO)
replaces `join`.

Node (`--role node`): the document of `deyroute join`.

### `deyroute join`

`{"schema", "role": "node", "node", "hub_name", "hub_addr", "hub_version",
"public_ip", "ca_fingerprint", "compatible", "config_path", "sysctl_profile",
"sysctl_warnings", "service_started", "steps": [Step]}`

### `deyroute status`

The `Status` DTO itself: `{"schema", "role", "version", "generated_at",
"hub": HubStatus?, "node": NodeSelf?, "tunnels": [TunnelInfo], "nodes":
[NodeInfo], "events": [Event] (newest first, at most 10), "warnings":
[Warning]?}`.

- HubStatus: `{"name", "public_ip", "control_port", "domain", "ui_mode",
  "language", "firewall", "acme_challenge", "acme_email", "telegram",
  "front"?}`
  (`firewall`: `managed` | `suggest-only`; `acme_challenge`: how
  `tls.mode acme` proves the domain, `http-01` | `dns-01` (Cloudflare token
  set) | `none` (HTTP-01 disabled and no token); `telegram`: `{"enabled",
  "chat_id", "token_file", "events"}` from `hub.notify.telegram`, never the
  token itself; `front`: the CDN front listener, absent when front mode was
  never configured: `{"enabled", "domain", "port", "listening" (the
  listener is bound: false while disabled or after DEY-X053), "cf_only" (the
  firewall opens the port to Cloudflare ranges only), "tls" (auto|custom|
  off)}`, never the path secret).
- NodeSelf: `{"id", "hub_addr", "connected", "last_contact", "hub_version",
  "compatible", "units"}`.
- Warning: `{"code", "message", "tunnel", "node"}`.

`status --watch --json` prints one compact Status document per refresh (every
2 seconds) until Ctrl-C; a failed refresh prints `{"schema": 1, "error":
ErrorDTO}` and the stream continues.

### TunnelInfo

`{"id", "name", "enabled", "state" (INIT|STARTING|UP|DEGRADED|SWITCHING|DOWN|
PAUSED|DISABLED), "active_node", "active_node_name", "active_transport",
"rtt_ms", "up_since", "ports": [{"listen", "proto", "target", "probe"}],
"nodes": [primary, backups…], "ladder_name", "ladder": [rung], "policy",
"paused", "service_down", "client_ip" (preserved|masked), "proxy_protocol"
(advanced.proxy_protocol: direct/haproxy keeps the client IP only with it),
"warnings", "traffic"?}`

- `traffic` (TrafficNow, absent while monitoring is off or before the first
  sample): `{"at", "available" (false: bytes cannot be counted on this hub,
  see DEY-X061), "rate_in_bit_s", "rate_out_bit_s" (bit/s over the last
  10-second sample), "today_in", "today_out" (bytes since 00:00 hub-local)}`.

### NodeInfo

`{"id", "name", "public_ip", "online", "control_rtt_ms", "version",
"compatible", "cpu_percent", "ram_bytes", "last_heartbeat", "country",
"udp_ok" (bool, absent when not tested), "tags", "cert_fingerprint",
"tunnels", "route" ("front" for a node that reaches the hub through the CDN
front, absent when direct; its `public_ip` may then be empty), "via"
(the transport of its current control stream: "front", absent for direct TCP or
when offline), "last_error" (the node agent's last error, absent when
none)}`

### Event

`{"seq", "at", "level" (info|warn|error), "type" (tunnel_up, switch_transport,
…), "tunnel", "node", "from_transport", "to_transport", "from_node",
"to_node", "reason", "code", "message"}`

`traffic_quota` (warn at 80 %, error at 100 % of a tunnel's
`advanced.monthly_quota_gib`, once per quota period) and `tune_drift` (warn:
a tuned kernel value was changed or overridden, DEY-X067) are event types
too; Telegram sends them when `hub.notify.telegram.events` selects `quota`
or `tuning` (or the full names).

## Nodes and hub

### `deyroute node join-command`

`{"schema", "command", "link", "expires_at"}`

### `deyroute node list`

`{"schema", "nodes": [NodeInfo]}`

### `deyroute node rename <id> <name>`

`{"schema", "ok": true, "node", "name"}`

### `deyroute node remove <id>`

`{"schema", "ok": true, "node"}`

### `deyroute node test <id>`

`{"schema", "node", "online", "control_rtt_ms", "udp_ok", "udp_rtt_ms",
"sysinfo": {string: string}}`

### `deyroute node set-hub <ip:port>`

`{"schema", "ok": true, "hub_addr", "daemon_running"}` — `daemon_running` is
false when the node agent was down and `node.hub_addr` was written directly.

### `deyroute hub announce-move <ip:port>`

`{"schema", "addr", "accepted": [node id], "offline": [node id], "front":
[node id]?}` (`front`: nodes behind the CDN front, which are never moved onto
a direct address: "front: unchanged")

## Tunnels

### `deyroute tunnel add`

`{"schema", "tunnel": TunnelInfo, "steps": [Step]}`

### `deyroute tunnel list`

`{"schema", "tunnels": [TunnelInfo]}`

### `deyroute tunnel show <id>`

The `TunnelDetail` DTO: every TunnelInfo field plus
`"failover": {"policy", "probe_interval_s", "probe_timeout_s",
"fail_threshold", "recover_threshold", "failback", "failback_after_s",
"max_switches_per_hour", "quarantine_s"}`, `"tls_mode"`, `"probe_port"`,
`"rungs": [{"node", "transport", "warm", "active", "control_port", "skipped",
"quarantine_until", "unit", "unit_state"}]`, `"probes": [{"at", "ok", "rtt"
(ns), "kind", "error"}]` (last 120 of the active candidate), `"metrics":
{"at", "bytes_in", "bytes_out", "active_conns", "source", "bytes_since"}?`,
`"failback_delay"` (ns) and `"events": [Event]`.

- `metrics.source` is `nft` when the hub counts bytes (traffic monitoring):
  `bytes_in` / `bytes_out` are then the real byte counters of the tunnel's
  listen ports since `bytes_since` (the last counter reset: a reboot or a
  rebuild of the accounting table). With `ss` only `active_conns` is
  measured and the byte fields are 0. For volumes per day or period use
  `deyroute stats`.

### `deyroute tunnel edit <id>`

`{"schema", "tunnel": TunnelInfo, "steps": [Step]}`

### `deyroute tunnel enable|disable <id>`

`{"schema", "ok": true, "tunnel", "enabled"}`

### `deyroute tunnel restart|reset|pause|resume <id>`

`{"schema", "ok": true, "tunnel"}`

### `deyroute tunnel delete <id>`

`{"schema", "ok": true, "tunnel", "steps": [Step]}`

### `deyroute tunnel switch <id>`

`{"schema", "ok": true, "tunnel", "transport", "node"}` (one of `transport`
and `node` is empty).

### `deyroute tunnel test-ladder <id>`

`{"schema", "tunnel", "results": [{"node", "transport", "ok", "rtt_ms",
"error": ErrorDTO?, "skipped"}], "steps": [Step]}`

### `deyroute tunnel backup add|remove <id> --node <nid>`

`{"schema", "ok": true, "tunnel", "node", "steps": [Step]}` (`steps` is empty
for `remove`).

## Ports and ladders

### `deyroute port add <tunnel> <port>`

`{"schema", "tunnel": TunnelInfo, "steps": [Step]}`

### `deyroute port remove <tunnel> <port>`

`{"schema", "tunnel": TunnelInfo}`

### `deyroute port set <tunnel> <ports> --probe <kind>`

`{"schema", "tunnel": TunnelInfo, "steps": [Step]}`. The new kinds are in
`tunnel.ports[].probe`; an invalid kind or a port the tunnel does not have
is the error document `DEY-C013`.

### `deyroute port check <port>`

The `PortCheckResult` DTO: `{"schema", "port", "proto", "bind_free",
"bind_process", "bind_addr", "bind_by_deyroute", "firewall_open",
"firewall_name", "firewall_command", "node", "node_reachable" (absent: not
tested), "node_rtt_ms", "node_error" (ErrorDTO `DEY-P014` when the node could
not connect; absent otherwise), "tunnel", "tunnel_ok" (absent: not tested),
"tunnel_rtt_ms", "note", "suggested_ports"}`

With `--open` (and `--yes`, since `--json` usually runs without a
terminal) the same document gains `"opened"` when a firewall blocked the
port: the last `PortOpenResult`, `{"port", "proto", "ran" (the command that
ran), "by" (the firewall it changed), "firewall_open", "firewall_name",
"firewall_command" (a firewall that still blocks), "note"}`. Without a
blocking firewall `"opened"` is absent. A port still closed after every
confirmed command is the error document `DEY-P013`; a firewall that changed
since the check is `DEY-P032`, a failing command `DEY-P033`.

### `deyroute port suggest`

`{"schema", "ports": [int]}`

### `deyroute ladder list`

`{"schema", "ladders": [{"name", "rungs", "builtin", "used_by"}]}`

### `deyroute ladder show <name>`

`{"schema", "name", "rungs", "builtin", "used_by"}`

### `deyroute ladder create|set <name> --rungs …`

`{"schema", "ok": true, "ladder", "rungs"}`

### `deyroute ladder delete <name>`

`{"schema", "ok": true, "ladder"}`

## Diagnostics

### `deyroute diag speed <tunnel>`

`{"schema", "result": {"tunnel", "transport", "seconds", "download_mbps",
"upload_mbps", "rtt_ms"}, "steps": [Step]}`

### `deyroute diag probe <tunnel>`

`{"schema", "tunnel", "probes": [{"tunnel", "port", "proto", "kind", "ok",
"rtt_ms", "error"}]}`

### `deyroute stats [<target>…] [--period 1h|24h|7d|30d] [--watch]`

The `TrafficReport` DTO: `{"schema", "generated_at", "period", "available",
"reason": ErrorDTO?, "timezone", "series": [TrafficSeries]}`. `--watch
--json` prints one compact document per refresh. Never a drawn chart.

- Targets: a tunnel id or `tunnel:<id>`, `node:<id>`, `hub`; none = every
  tunnel. An unknown period or a malformed target is `DEY-C027` (exit 1).
- `available` is false when the hub cannot count bytes (no nft, no
  nf_tables, `monitoring.enabled: false`); `reason` is then `DEY-X061`.
  Host series (CPU/RAM) keep working.
- `timezone`: the hub-local zone of "today" and of the quota period (IANA
  name, or `UTC+03:30` when the hub has none).
- TrafficSeries: `{"id" (tunnel or node id, or "hub"), "kind"
  (tunnel|node|hub), "name", "available", "reason": ErrorDTO?, "step_s"
  (width of one point), "points": [TrafficPoint], "totals"?}`.
- TrafficPoint: `{"at" (bucket start), "bytes_in", "bytes_out", "conns",
  "cpu_percent", "ram_bytes", "gap"}`. Tunnel points carry bytes and
  `conns` (maximum of established TCP connections; absent = unknown, e.g.
  UDP), node and hub points `cpu_percent` and `ram_bytes`. A missing number
  is 0. `gap: true` means no valid sample (hub stopped, tunnel not up,
  counter reset, clock jump): draw it as missing, not as zero. Points are
  downsampled to at most 120 (bytes summed, conns/CPU/RAM maximum).
- Totals (tunnel series): `{"today_start", "today_in", "today_out",
  "days30_in", "days30_out" (rolling 30 days), "period_start", "period_in",
  "period_out" (since the start of the quota period,
  `monitoring.quota_reset_day`), "quota_bytes" (advanced.monthly_quota_gib
  in bytes, absent = no quota)}`.
- Bytes are L3 bytes on the hub's user-facing listen ports: they differ
  from payload, and the provider usually bills the hub's network interface,
  which also carries the tunnel leg (about twice the user-side volume).

### `deyroute logs [<tunnel>|hub|node]`

One compact document per log line: `{"schema": 1, "source": "hub"|"node",
"line": "…"}` (secrets are masked by the daemon). With `-f` the stream runs
until Ctrl-C.

### `deyroute events`

`{"schema", "events": [Event]}` (newest first).

### `deyroute doctor`

`{"schema", "path" (the written .tar.gz), "role", "daemon_running", "node"
(the --node argument or ""), "findings": [{"rule" (R01…R15), "severity"
(ok|info|warn|error), "message", "fix"}]}`

## System

### `deyroute optimize apply|revert`

`{"schema", "profile", "bbr_available", "bbr_active", "applied": {key:
value}, "warnings": [string], "mem_bytes" (the hub's RAM), "recommended"
(balanced, or aggressive from 4 GB RAM), "facts": TuneFacts?, "nodes":
[NodeTuneStatus]?}` — a warning of a node starts with `node <id>: `.
`profile` may also be `auto`.

- TuneFacts: `{"mem_bytes", "mem_available_bytes", "cpus", "kernel", "virt"
  (openvz|lxc|docker|container, absent on a VM or bare metal),
  "bbr_available", "fq_available", "qdisc", "conntrack_loaded",
  "conntrack_max", "conntrack_count", "nic", "nic_mtu", "nic_speed_mbps"}`.
- NodeTuneStatus: `{"node", "online", "profile", "hash", "pending" (the node
  still has to apply the hub's tuning), "auto_capable" (false for an agent
  too old for `auto`)}`.

### `deyroute optimize auto [--dry-run] [--backends]`

The `TunePlanReport` DTO: `{"schema", "hash", "hosts": [TuneHost],
"applied" (true after the apply), "warnings"}`; after an apply also
`"steps": [Step]`. With `--dry-run`, or when no host has a change and no node
is pending, it is the plan and nothing changes; otherwise the plan is
applied after one confirmation (`--yes` for scripts; without a terminal and
without `--yes` the plan goes to stderr and the command exits 3). A plan
that changed since it was shown is refused with `DEY-X065`; run the command
again to see the new plan. `optimize apply --profile auto` is
`optimize auto --yes`.

- TuneHost: `{"host" ("hub" or the node id), "role" (hub|node), "facts":
  TuneFacts?, "changes": [TuneChange], "skips": [TuneSkip], "hash",
  "pending" (an offline node: it applies when it reconnects), "error":
  ErrorDTO?}`.
- TuneChange: `{"kind" (sysctl|sysfs|modules|dropin|backend|firewall),
  "key", "from" ("" = absent), "to", "reason", "effect"
  (now|next-start|reboot|restarts-tunnels), "raise_only"}`.
  `restarts-tunnels` items are part of the plan only with `--backends`.
- TuneSkip: `{"key", "reason", "code"}` (`DEY-X064` for kernel items in a
  container).

### `deyroute optimize check`

The `TuneCheck` DTO: `{"schema", "clean", "hosts": [{"host", "role",
"profile", "drift": [{"key", "want", "live", "overridden_by" (the file that
sets the key later, absent for a runtime change)}], "findings": [{"check",
"severity" (info|warn|error), "message", "code"}], "error": ErrorDTO?}]}`.
Drift exits with code 2 (`DEY-X067`, for the first drifted key): the same
document then also carries `"error"` (ErrorDTO) and `"exit_code": 2`, so
stdout still holds one document. Findings alone are reported with exit 0.

### `deyroute optimize status`

The `OptimizeStatus` DTO, as `optimize apply|revert` prints it (with
`facts` and one `nodes` row per node).

### `deyroute security rotate-tokens`

`{"schema", "ok": true, "tunnel" ("" = every tunnel), "steps": [Step]}`

### `deyroute security rotate-ca`

`{"schema", "reissued": [node id], "offline": [node id], "steps": [Step]}`

### `deyroute security tls show|renew`

`{"schema", "certificates": [{"tunnel", "kind" (ca|hub|node|tunnel), "mode",
"subject", "sans", "not_after", "days_left", "fingerprint", "warning"}]}`

### `deyroute security tls domain <name>|--clear`

`{"schema", "ok": true, "domain"}` (`""` after `--clear`).

### `deyroute security tls acme [--email E] [--cloudflare-token-file F]`

`{"schema", "ok": true, "email"?, "cloudflare_token_file"?}` — only the
settings that were given; `cloudflare_token_file` is where the token is
stored (`/etc/deyroute/secrets/cloudflare.token`, or `""` when it was
removed), never the token.

### `deyroute security firewall show|apply|disable`

`{"schema", "managed", "detected": [nftables|ufw|firewalld|iptables],
"ruleset", "suggested": [command]}`

### `deyroute security audit`

`{"schema", "clean", "items": [{"check", "severity" (ok|warn|error),
"message"}]}`

### `deyroute notify telegram set`

`{"schema", "ok": true, "token_file", "chat_id"}`

### `deyroute notify telegram test|off`

`{"schema", "ok": true}`

### `deyroute backup`

`{"schema", "path", "encrypted", "events", "events_error": ErrorDTO?}` —
`events` is the number of exported events; `0` on a node (nodes keep no event
history); `-1` when the history could not be exported, `events_error` then
says why (`DEY-X003`: the daemon was not running). The backup itself is
written either way.

### `deyroute restore FILE`

`{"schema", "role", "hub_name", "node", "public_ip", "address_changed",
"backup_version", "created", "previous_dir", "migrated", "sysctl_profile",
"service_started", "steps": [Step]}`

### `deyroute update`

- `--check`, or nothing to update: the UpdateInfo DTO `{"schema", "current",
  "latest", "available", "changelog", "previous"}`. With a newer release,
  `changelog` is the text of its `CHANGELOG.md` after the running version
  (at most 40 lines, then `… <release page>`), or the release page address
  when the release has no verified `CHANGELOG.md`.
- update applied: `{"schema", "update": UpdateInfo, "steps": [Step]}`. With
  `--version V` a failed release check does not stop the update (a note goes
  to standard error); without it the check's error is the result.
- `--rollback`: UpdateInfo.

### `deyroute update backends [name]`

`{"schema", "backends": [{"backend", "from", "to", "status"
(updated|rolled_back|unchanged|failed), "error": ErrorDTO?}], "steps":
[Step]}`

### `deyroute update manifest`

`{"schema", "source" (embedded | /etc/deyroute/backends.yaml), "versions":
{backend: version}}`

### `deyroute config show`

`{"schema", "path": "/etc/deyroute/config.yaml", "config": {…}}` — `config` is
the file converted from YAML as written (no defaults added); like every
output, its string values pass the central secret filter (a registered
secret prints as `***`).

### `deyroute config validate`

`{"schema", "path", "valid": true, "role", "tunnels" (count), "nodes"
(count)}`; an invalid file is the error document with every problem in
`errors`.

### `deyroute config edit`

- saved and applied: `{"schema", "path", "saved": true, "apply": {"backup",
  "changed", "warnings"}, "steps": [Step]}`;
- saved on a node (the node agent has no apply; it reads `node.hub_addr` on
  its next reconnect and everything else when it restarts): `{"schema",
  "path", "saved": true, "restart": "deyroute-node"}`;
- `config.yaml` changed while the editor was open: the error document with
  `DEY-C024` (nothing is saved; the edited copy is kept, its path is in the
  `fix` text);
- nothing saved: `{"schema", "path", "saved": false, "reason": "unchanged" |
  "aborted"}`.

### `deyroute config apply`

`{"schema", "apply": {"backup", "changed": [string], "warnings": [string]},
"steps": [Step]}`

### `deyroute settings ui-mode <mode>`

`{"schema", "ok": true, "ui_mode"}`

### `deyroute uninstall`

`{"schema", "ok": true, "kept_backups", "nodes": [node id uninstalled
first], "steps": [Step]}`

### `deyroute completion`

Prints a shell script; `--json` does not apply.
