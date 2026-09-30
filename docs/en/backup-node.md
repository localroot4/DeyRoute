# Backup node and failover

[فارسی](../fa/backup-node.md) · [Index](index.md)

A backup node is a second foreign server for the same tunnel. When the
primary node fails, the same hub ports are served by the backup node; when the
primary is healthy again, the tunnel returns to it.

> **Backup only works if the same service runs on both nodes.**
> The backup must have the same service with the same users and settings
> (a Marzban node, or a copy of the Xray config). DEYROUTE moves the traffic;
> it does not copy your VPN users.

## Add a backup node

1. Join the second server as a node ([Join](join.md)); say its id is `nl-1`.
2. Add it to the tunnel:

   ```bash
   deyroute tunnel backup add main --node nl-1
   ```

   Menu: `5) Failover` → `3) Backup nodes` → choose the tunnel →
   `1) Add backup node`.

Every rung of the tunnel's ladder is installed and warmed on the backup node,
then you see:

```text
backup nl-1 ready (warm)
Backup only works if the same service runs on both nodes.
```

You can also name backups when creating the tunnel:
`deyroute tunnel add --node de-1 --ports 443,2053 --name main --backup nl-1`.
The order is fixed: the first node is the primary, the others are backups in
the order they were added. `deyroute tunnel show main` shows them as
`de-1 (primary), nl-1 (backup)`.

Remove a backup node:

```bash
deyroute tunnel backup remove main --node nl-1
```

## How failover works

Every tunnel has exactly one active transport on one node. All other rungs —
on the primary and on every backup node — are **warm**: the binary is
installed, the config rendered and the systemd unit created but stopped, so
they use no memory. A switch is therefore only "stop the active unit, start
the next one".

The hub probes the active transport every 5 seconds: it connects to its own
tunnel port and checks that the service on the node answers through the
tunnel. With the default settings:

| What happens | Result |
| --- | --- |
| 1 or 2 failed probes in a row (or RTT above 3× the 10-minute median for 60 s) | `DEGRADED` (yellow); nothing changes yet |
| 3 failed probes in a row (`fail_threshold`) | `SWITCHING`: the next candidate is started |
| the candidate passes a probe within 15 s | `UP` again; event `switch_transport` or `switch_node` |
| the candidate fails | it is quarantined for 10 minutes (doubling up to 1 hour) and the next one is tried |
| every candidate failed | `DOWN` (`DEY-F001`); the whole ladder is retried from rung 1 after 30 s, backing off to every 5 minutes |

Targets from the specification: at most **35 seconds** (p95) from the moment
filtering blocks the active transport until the tunnel is UP on the next rung,
and at most **3 seconds** of interruption for users during the switch.
Measured values are published in [Benchmarks](benchmarks.md) once available.
When the tunnel moves to another node, every user session is cut and
reconnects; that is normal.

### Which candidate comes next (policy)

| Policy | Order |
| --- | --- |
| `transport_then_node` (default) | the next rungs on the same node; when every rung of that node was tried, the next node from rung 1 |
| `transport_only` | only the rungs of the current node; never another node |
| `node_only` | the same rung on the next node (useful when the transport is fine and the servers are the problem) |

Change it with `deyroute tunnel edit main --policy node_only` or
`5) Failover` → `1) Policy`.

### When the tunnel goes to the backup node

- **All rungs of the primary failed** in this round (policy
  `transport_then_node`).
- **The primary node is gone**: its control connection has been offline for
  more than 30 seconds and the tunnel probe fails. Its other rungs are
  skipped and the backup node is tried from rung 1 at once.
- **The service on the node is down** (for example Xray stopped): changing the
  transport would not help, so the transport is not changed; the tunnel moves
  to the same rung on the backup node and a `service_down` event is recorded.
  Without a backup node the tunnel stays `DEGRADED` (service down) with
  `DEY-F005`: start the service on the node.

When only the control connection is down but the tunnel still passes
traffic, nothing switches; you only see a `node_offline` event.

## Failback: returning to the primary

The tunnel always returns to **rung 1 on the primary node**, not to the
previous rung.

- While the tunnel runs elsewhere, the hub keeps a small test copy of rung 1
  (the *canary*) towards the primary node. It carries no user ports. When the
  canary passes 6 probes in a row (`recover_threshold`), the tunnel moves
  back.
- If a canary cannot be built, the tunnel moves back after it has been stable
  for 300 seconds (`failback_after_s`).
- The primary node must be online and its service must answer.
- If rung 1 does not pass its probe within 15 seconds, the tunnel returns to
  where it was at once, records `failback_failed` (`DEY-F003`) and doubles the
  waiting time for this tunnel (at most 24 hours; reset after a successful
  failback).

## Protection against flapping

More than 6 automatic switches in one hour (`max_switches_per_hour`) stops
automatic switching: the tunnel stays where it is if it is UP, or goes to
`direct/native`, and a `flapping` event is recorded (`DEY-F002`). Manual
switches are always allowed and do not count.

## Manual control

| Command | Menu | What it does |
| --- | --- | --- |
| `deyroute tunnel switch main --transport backhaul/tcpmux` | `2) Tunnels` → `5) Switch transport` | switch to a transport now |
| `deyroute tunnel switch main --node nl-1` | same | switch to another node now |
| `deyroute tunnel reset main` | `5) Failover` → `5) Reset to rung 1` | go back to rung 1 on the primary node now |
| `deyroute tunnel pause main` | `5) Failover` → `4) Pause / resume failover` | stop automatic switching (probes continue), e.g. during maintenance |
| `deyroute tunnel resume main` | same | start automatic switching again |
| `deyroute tunnel test-ladder main` | `5) Failover` → `6) Test ladder` | try every rung for 20 seconds and report RTT and success |

`test-ladder` interrupts the tunnel for the whole test (users lose their
connections), so it asks you to type `yes`; in scripts add `--yes`. A paused
tunnel shows `PAUSED` on the dashboard until you resume it; manual switches
still work while it is paused.

## Rungs and their states

`deyroute tunnel show main` lists every rung on every node of the tunnel:

| State | Meaning |
| --- | --- |
| `active` | carries the traffic now |
| `warm` | ready; can be started in a moment |
| `skipped: <reason>` | cannot be used for this tunnel now (e.g. UDP blocked); re-tested every 30 minutes |
| `quarantined until <time>` | failed recently; not tried again before that time |
| `not rendered` | not prepared on that node yet |

## Thresholds (Advanced mode)

In Advanced mode `5) Failover` → `7) Thresholds *` changes, per tunnel:

| Setting | Default |
| --- | --- |
| Probe interval (seconds) — `probe_interval_s` | 5 |
| Probe timeout (seconds) — `probe_timeout_s` | 3 |
| Failed probes before a switch — `fail_threshold` | 3 |
| Good probes to recover — `recover_threshold` | 6 |
| Fail back to rung 1 — `failback` | yes |
| Fail back after (seconds) — `failback_after_s` | 300 |
| Max automatic switches per hour — `max_switches_per_hour` | 6 |
| Quarantine of a failed transport (seconds) — `quarantine_s` | 600 |

The same keys live under `failover:` of each tunnel in `config.yaml`
(`deyroute config edit`).

## Events to watch

```bash
deyroute events --tunnel main --since 24h
```

`switch_transport`, `switch_node`, `failback`, `failback_failed`, `flapping`,
`service_down`, `node_offline`, `node_online`, `tunnel_down`, `tunnel_up`,
`rung_skipped`. With Telegram enabled (`9) Notifications`) you get one
message per event.
