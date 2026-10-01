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
  "language", "firewall", "acme_challenge", "acme_email", "telegram"}`
  (`firewall`: `managed` | `suggest-only`; `acme_challenge`: how
  `tls.mode acme` proves the domain, `http-01` | `dns-01` (Cloudflare token
  set) | `none` (HTTP-01 disabled and no token); `telegram`: `{"enabled",
  "chat_id", "token_file", "events"}` from `hub.notify.telegram`, never the
  token itself).
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
"warnings"}`

### NodeInfo

`{"id", "name", "public_ip", "online", "control_rtt_ms", "version",
"compatible", "cpu_percent", "ram_bytes", "last_heartbeat", "country",
"udp_ok" (bool, absent when not tested), "tags", "cert_fingerprint",
"tunnels", "last_error" (the node agent's last error, absent when none)}`

### Event

`{"seq", "at", "level" (info|warn|error), "type" (tunnel_up, switch_transport,
…), "tunnel", "node", "from_transport", "to_transport", "from_node",
"to_node", "reason", "code", "message"}`

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

`{"schema", "addr", "accepted": [node id], "offline": [node id]}`

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
{"at", "bytes_in", "bytes_out", "active_conns", "source"}?`,
`"failback_delay"` (ns) and `"events": [Event]`.

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
(balanced, or aggressive from 4 GB RAM)}` — a warning of a node starts with
`node <id>: `.

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
