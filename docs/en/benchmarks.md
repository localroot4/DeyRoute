# Benchmarks

[Index](index.md)

This page records how DEYROUTE's resource use (specification section 12) and
failover times (section 9) are measured, and the results. There are two kinds
of numbers:

- **Lab** — the integration scenarios of `test/integration` (the section 17
  test bed in Docker containers with systemd, on one host). They prove the
  mechanisms and the limits on a fast local network; they say nothing about
  real servers, real filtering or the 1 vCPU / 1 GB budget.
- **Real servers** — a hub and nodes with the budget of section 12. **Not
  measured yet**: those cells say "pending". Numbers are only added from real
  runs, together with the version, the hardware and the raw data.

## Test setup

| Item | Value to record |
| --- | --- |
| deyroute version | `deyroute version` on hub and nodes (version, commit, build date, Go) |
| Backend versions | pinned in `internal/backend/backends.yaml` (see the [backend pages](../backends/backhaul.md)) |
| Servers | hub and nodes with **1 vCPU and 1 GB RAM** (the budget of section 12), Ubuntu 24.04 or Debian 12 |
| Topology (resources) | 1 hub, 2 nodes, 3 tunnels with default settings |
| Topology (failover) | the section 17 test bed: `hub`, `node1`, `node2` and `client` of `test/integration/compose.yml`; the scenario scripts are `test/integration/scenarios/<id>.sh` (S01–S33). The blocker drops traffic with nftables in the hub's network namespace (`lib.sh` `block`) and the client is a real Xray VLESS+ws+tls client (`lib.sh` `vpn_up`); see QUESTIONS.md C.37 |
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
failed requests (from the first failed request to the next good one). In the
lab this is `start_prober` of `test/integration/lib.sh`: the Xray client on
the client container, `curl --proxy … --proxytunnel` every 200 ms.

The blocker drops the traffic of the active rung between hub and node with
nftables: find the rung's control port in `deyroute tunnel show main` (RUNGS →
CTL PORT) and drop it (`nft add rule … tcp dport <ctl> drop`, `udp` too for
UDP rungs). Times come from the events (`deyroute events --tunnel main --json`,
field `at`) and from the client log.

| Measurement | Goal | Procedure |
| --- | --- | --- |
| Block → UP on the next rung (S08) | ≤ 35 s at p95 | apply the block at t0; t1 = time of the `switch_transport` event (the tunnel is UP again); repeat 10 times; report p50, p95, max |
| User interruption during a switch (S08) | ≤ 3 s | from t1 to the client's first good request after it (`lib.sh` `recovery_ms`); the longer outage from the block to that request (detection plus switch) is reported as well |
| Manual switch between backends (phase 4, S32) | ≤ 3 s interruption | `deyroute tunnel switch main --transport rathole/noise`, then `frp/tcp`, then back; client interruption for each |
| Failback after the block is removed (S09) | ≤ `failback_after_s` + 20 s | remove the block at t2; t3 = `failback` event; with a canary the failback may come earlier |
| Failed failback (S10) | immediate return, delay doubled | block again at the moment of failback; expect `failback_failed` and a doubled delay in `deyroute tunnel show main` |
| Service down on the primary node (S11) | switch to the backup node without a transport switch | stop the service on node1; expect `service_down` and `switch_node` with the same transport |
| Primary node network cut (S12) | backup node UP ≤ 45 s | cut all traffic of node1 at t0; t1 = `switch_node` event |
| Seven blocks in one hour (S13) | `flapping`, tunnel stays on `direct/native` | repeat the S08 block 7 times within 60 minutes |
| Update of deyroute (S19) | no lost requests | run `deyroute update` while the client runs; count failed requests |

Report for every run: date, deyroute version and commit, server type and
provider, the ladder, the raw client log and the events export.

## Results

### Lab results (2026-10-01)

deyroute `0.9.0-it.1` built by `test/integration/run.sh` from the tree of
2026-10-01 (Go 1.26.8, linux/amd64); the backends pinned in `backends.yaml`,
downloaded through the build environment's HTTPS proxy. Containers from an
Ubuntu 24.04 image with systemd on one host (Linux 6.18; the kernel has no
WireGuard module). One scenario at a time; the logs are
`dist/integration/<id>.log` and `results-<distro>.txt` of `run.sh`.

| Measurement | Goal | Lab result | Runs |
| --- | --- | --- | --- |
| One-line install, hub / node (S01) | < 60 s | 1 s / 1 s | 1 |
| Join: installer start → node online (S01) | < 30 s | 1.4 s | 1 |
| Join: `deyroute join` → node online (S03) | < 30 s | 1.0 s | 1 |
| 100 MB through the Xray VLESS+ws+tls client and `backhaul/wssmux` (S04) | intact, no TLS error | intact in 0.62 s, no TLS error | 1 |
| Backend killed with `kill -9` → back (S07) | < 5 s | hub 4675 ms, node 3070 ms | 1 |
| Block → UP on the next rung (S08) | ≤ 35 s at p95 | p50 19 s, p95 20 s, max 20 s | 10 |
| Client back after the switch (S08) | ≤ 3 s | 69–737 ms (median 228 ms) | 10 |
| Client outage from the block (S08; detection plus switch) | — | 16.0–16.6 s | 10 |
| Manual switch backhaul/wssmux → rathole/noise → frp/tcp → backhaul/wssmux (S32) | ≤ 3 s each | 1472 ms, 0 ms, 0 ms (UP after 3.2 s, 1.2 s, 1.2 s) | 1 × 3 |
| Failback after unblock (S09, `failback_after_s` 60) | ≤ 80 s | 38 s after the unblock (the failback delay runs from the switch) | 1 |
| Backup node after network cut (S12) | ≤ 45 s | 23 s | 1 |
| Lost requests during the update of hub and node (S19) | 0 | 0 of 67 | 1 |
| Backhaul with 500 concurrent connections (S25, `backhaul/tcpmux`) | ≤ 150 MB, no errors | 500 of 500 complete; peak memory of the unit: hub 34 MB, node 35 MB | 1 |
| Node not an open proxy (S17) | 0 packets to 8.8.8.8:53 | 0 with xray/reality, hysteria2/udp, awg/userspace, direct/native | 1 |
| awg/userspace carries TCP and UDP; `tunnel delete` leaves nothing (S33) | — | TCP 8443 and UDP 27015 carried; ruleset and interfaces equal before and after | 1 |

### Resource budget

| Item | Limit | Lab | Real servers (1 vCPU, 1 GB) |
| --- | --- | --- | --- |
| `deyroute-hub` RSS idle (3 tunnels, 2 nodes) | ≤ 60 MB | not measured | pending |
| `deyroute-node` RSS idle | ≤ 35 MB | not measured | pending |
| Hub CPU idle, 5-minute average | ≤ 1 % | not measured | pending |
| Warm transport RAM | 0 | not measured | pending |
| Active transport RAM (Backhaul, idle) | 20–50 MB | not measured | pending |
| Backhaul with 500 connections (S25) | ≤ 150 MB | hub 34 MB, node 35 MB, 0 errors | pending |
| Log rotation | 20 MB × 5 | not measured | pending |
| `state.db` size | ≤ 50 MB | not measured | pending |
| Binary size (`-trimpath -s -w`, Go 1.26.8, 2026-10-01) | ≤ 30 MB | amd64 17,608,866 B (16.8 MiB), arm64 15,990,946 B (15.3 MiB) | same binary |

### Failover on real servers

| Measurement | Goal | p50 | p95 | max | Runs |
| --- | --- | --- | --- | --- | --- |
| Block → UP on next rung (S08) | ≤ 35 s (p95) | pending | pending | pending | pending |
| User interruption at switch (S08) | ≤ 3 s | pending | pending | pending | pending |
| Manual switch interruption | ≤ 3 s | pending | pending | pending | pending |
| Failback after unblock (S09) | ≤ `failback_after_s` + 20 s | pending | pending | pending | pending |
| Backup node after network cut (S12) | ≤ 45 s | pending | pending | pending | pending |
| Lost requests during update (S19) | 0 | pending | pending | pending | pending |

| Scenario | Expected | Result |
| --- | --- | --- |
| S10 failed failback | immediate return, delay doubled | lab S10; real servers pending |
| S11 service down on primary | backup node, same transport | lab S11; real servers pending |
| S13 seven blocks in one hour | `flapping`, stays on `direct/native` | lab S13; real servers pending |
