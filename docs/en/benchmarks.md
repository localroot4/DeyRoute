# Benchmarks

[Index](index.md)

This page records how DEYROUTE's resource use (specification section 12) and
failover times (section 9) are measured, and the results. **Nothing has been
measured yet**: every result cell says so. Numbers are only added from real
runs, together with the version, the hardware and the raw data.

## Test setup

| Item | Value to record |
| --- | --- |
| deyroute version | `deyroute version` on hub and nodes (version, commit, build date, Go) |
| Backend versions | pinned in `internal/backend/backends.yaml` (see the [backend pages](../backends/backhaul.md)) |
| Servers | hub and nodes with **1 vCPU and 1 GB RAM** (the budget of section 12), Ubuntu 24.04 or Debian 12 |
| Topology (resources) | 1 hub, 2 nodes, 3 tunnels with default settings |
| Topology (failover) | the section 17 test bed: containers or VMs `hub`, `node1`, `node2`, `blocker`, `client`; the scenario scripts belong in `test/integration/scenarios/<id>.sh` (the directory is still empty, so the procedure below is the reference) |
| Kernel profile | `balanced` (`deyroute optimize apply --profile balanced`) |

Let the system run for 10 minutes after the last change before measuring
anything "idle".

## Resource budget (section 12) — method

| Budget item | Limit | How to measure |
| --- | --- | --- |
| RSS of `deyroute-hub`, idle, 3 tunnels, 2 nodes | ≤ 60 MB | `pid=$(systemctl show -p MainPID --value deyroute-hub)`; sample `ps -o rss= -p $pid` every 10 s for 5 minutes; report max and median |
| RSS of `deyroute-node`, idle | ≤ 35 MB | same with `deyroute-node` on a node |
| CPU of the hub, idle (probes every 5 s) | ≤ 1 % average over 5 minutes | read fields 14+15 (utime+stime) of `/proc/$pid/stat` at t0 and t0+300 s; CPU % = Δticks ÷ (`getconf CLK_TCK` × 300) × 100 |
| RAM of a warm (stopped) transport | 0 | `systemctl list-units 'deyroute-tun@*' --state=running` shows only active transports (and canary units); a warm unit has no process |
| RAM of an active transport | per the section 7 table (e.g. Backhaul 20–50 MB, Rathole 5–15 MB) | `systemctl show -p MemoryCurrent <unit>` and the RSS of its main PID, idle and under load |
| Backhaul with 500 concurrent connections (scenario S25) | ≤ 150 MB, no errors | the client opens 500 connections through `hub:443` and keeps them busy for 5 minutes; record the peak `MemoryCurrent` of the active unit and the client error count |
| Log size | rotate at 20 MB, keep 5 compressed files | run with `--debug` until more than 100 MB were logged; `ls -l /var/log/deyroute/` |
| `state.db` | ≤ 50 MB | `du -b /var/lib/deyroute/state.db` after 24 h and after more than 5000 events |
| `deyroute` binary size | ≤ 30 MB (no UPX) | `stat -c %s /usr/local/bin/deyroute` (`make build-all` also enforces it) |

## Failover (section 9) — method

The client sends one request every **200 ms** through `hub:443` (a real
VLESS+ws+tls client, e.g. Xray, with `curl --proxy` in a loop) and logs the
time and result of every request. An *interruption* is the longest run of
failed requests, multiplied by 200 ms.

The blocker drops the traffic of the active rung between hub and node with
nftables: find the rung's control port in `deyroute tunnel show main` (RUNGS →
CTL PORT) and drop it (`nft add rule … tcp dport <ctl> drop`, `udp` too for
UDP rungs). Times come from the events (`deyroute events --tunnel main --json`,
field `at`) and from the client log.

| Measurement | Goal | Procedure |
| --- | --- | --- |
| Block → UP on the next rung (S08) | ≤ 35 s at p95 | apply the block at t0; t1 = time of the `switch_transport` event (the tunnel is UP again); repeat 10 times; report p50, p95, max |
| User interruption during a switch (S08) | ≤ 3 s | longest failed run of the client around t1 |
| Manual switch between backends (phase 4) | ≤ 3 s interruption | `deyroute tunnel switch main --transport rathole/noise`, then `frp/…`, then back; client interruption for each |
| Failback after the block is removed (S09) | ≤ `failback_after_s` + 20 s | remove the block at t2; t3 = `failback` event; with a canary the failback may come earlier |
| Failed failback (S10) | immediate return, delay doubled | block again at the moment of failback; expect `failback_failed` and a doubled delay in `deyroute tunnel show main` |
| Service down on the primary node (S11) | switch to the backup node without a transport switch | stop the service on node1; expect `service_down` and `switch_node` with the same transport |
| Primary node network cut (S12) | backup node UP ≤ 45 s | cut all traffic of node1 at t0; t1 = `switch_node` event |
| Seven blocks in one hour (S13) | `flapping`, tunnel stays on `direct/native` | repeat the S08 block 7 times within 60 minutes |
| Update of deyroute (S19) | no lost requests | run `deyroute update` while the client runs; count failed requests |

Report for every run: date, deyroute version and commit, server type and
provider, the ladder, the raw client log and the events export.

## Results

### Resource budget

| Item | Limit | Result |
| --- | --- | --- |
| `deyroute-hub` RSS idle (3 tunnels, 2 nodes) | ≤ 60 MB | not measured yet |
| `deyroute-node` RSS idle | ≤ 35 MB | not measured yet |
| Hub CPU idle, 5-minute average | ≤ 1 % | not measured yet |
| Warm transport RAM | 0 | not measured yet |
| Active transport RAM (Backhaul, idle) | 20–50 MB | not measured yet |
| Backhaul with 500 connections | ≤ 150 MB | not measured yet |
| Log rotation | 20 MB × 5 | not measured yet |
| `state.db` size | ≤ 50 MB | not measured yet |
| Binary size | ≤ 30 MB | not measured yet |

### Failover

| Measurement | Goal | p50 | p95 | max | Runs |
| --- | --- | --- | --- | --- | --- |
| Block → UP on next rung (S08) | ≤ 35 s (p95) | not measured yet | not measured yet | not measured yet | not measured yet |
| User interruption at switch (S08) | ≤ 3 s | not measured yet | not measured yet | not measured yet | not measured yet |
| Manual switch interruption | ≤ 3 s | not measured yet | not measured yet | not measured yet | not measured yet |
| Failback after unblock (S09) | ≤ `failback_after_s` + 20 s | not measured yet | not measured yet | not measured yet | not measured yet |
| Backup node after network cut (S12) | ≤ 45 s | not measured yet | not measured yet | not measured yet | not measured yet |
| Lost requests during update (S19) | 0 | not measured yet | not measured yet | not measured yet | not measured yet |

| Scenario | Expected | Result |
| --- | --- | --- |
| S10 failed failback | immediate return, delay doubled | not measured yet |
| S11 service down on primary | backup node, same transport | not measured yet |
| S13 seven blocks in one hour | `flapping`, stays on `direct/native` | not measured yet |
