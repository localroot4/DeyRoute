# Acceptance evidence (spec sections 16 and 18)

[Index](index.md)

Every acceptance criterion of spec section 16 and every item of the delivery
checklist (section 18) with the test that proves it. Three kinds of evidence:

- **unit** — `go test -race ./...` (CI job `unit`, every push).
- **lab** — a scenario of `test/integration` (real systemd, nftables and
  backends in containers; CI job `integration` on `main`/tags or by hand,
  `test/integration/run.sh` locally). "lab, run" means it passed in a recorded
  run; the backend scenarios were run with the real pinned backends. The
  numbers of the run of 2026-10-01 are in [benchmarks](benchmarks.md#lab-results-2026-10-01);
  every CI run uploads `results-<distro>.txt` and the scenario logs as the
  artifact `integration-<distro>`. The lab's client is a real Xray
  VLESS+ws+tls client and its blocker runs nftables in the hub's network
  namespace (QUESTIONS.md C.37).
- **pending** — needs real servers, a real user or a recording and cannot be
  produced from the build environment (QUESTIONS.md C.13).

## Section 16 — phases

| Phase | Criterion | Evidence |
| --- | --- | --- |
| 0 | static amd64/arm64 build green in CI | CI `build`; `release-edge` publishes both |
| 0 | binary runs on Ubuntu 24.04 / Debian 12, menu opens | CI `smoke` (5 Tier 1 images), `test/smoke/menu.sh` |
| 1 | one-line install on each Tier 1 VM < 60 s | lab S01, run (1 s per server); VMs: **pending** |
| 1 | re-running the installer upgrades, config untouched | lab S02, run |
| 1 | unknown config key gives `DEY-C001` | unit `internal/config` |
| 1 | no secret in logs (automatic grep) | lab S26, run; unit `internal/log` redaction |
| 2 | join < 30 s | lab S01 and S03 assert it, run (node online 1.4 s after the one-line installer started, 1.0 s after `deyroute join`) |
| 2 | `tunnel add --node de-1 --ports 443` UP, a real VLESS+ws+tls client downloads 100 MB | lab S04, run with real Backhaul (first rung) and a real Xray client and server: 100 MB intact in 0.6 s, no TLS error, the service on the node never sees the client (it sees 127.0.0.1), the traffic leaves at the node |
| 2 | backend killed → back in < 5 s | lab S07, run (hub 4.7 s, node 3.1 s, `backend_crash` event) |
| 2 | hub without GitHub gets binaries via the node | lab S06, run |
| 3 | an untrained person reaches UP in < 5 min with the menu | **pending** (real-user test, recording) |
| 3 | busy port shows the process name | lab S05, run (`DEY-P012` names nginx) |
| 3 | 80 columns without UTF-8 do not break the menu | lab S24, run; unit golden files `internal/tui/testdata` |
| 4 | manual switch between 3 backends, outage ≤ 3 s | lab S32, run: backhaul/wssmux → rathole/noise → frp/tcp → back with the Xray client every 200 ms, longest outages 1472, 0 and 0 ms; unit `internal/failover` |
| 4 | golden render tests per transport | unit `internal/backend/*` |
| 4 | `test-ladder` reports RTT per rung | lab S18, run with real backends (Backhaul, Rathole, Xray-Reality, Hysteria2, direct pass) |
| 5 | blocked rung → UP on the next ≤ 35 s (p95 of 10) | lab S08, run (10 runs: p50 19 s, p95 20 s; the Xray client is back 69–737 ms after each switch) |
| 5 | block lifted → failback ≤ `failback_after_s` + 20 s | lab S09, run (38 s after the unblock with `failback_after_s` 60) |
| 5 | service down on the primary → backup node, same transport | lab S11, run |
| 5 | 7 blocks in an hour → flapping, held on `direct/native` | lab S13 |
| 5 | hub restart during failover → state recovered, no restart | lab S14, run |
| 5 | Telegram message per event | unit `internal/notify`, `internal/daemon/hub` (no bot in the lab) |
| 6 | Xray-Reality, Hysteria2, Waterwall in the ladder | lab S18 (Waterwall needs a real decoy certificate: its node side trusts only the CA list built into Waterwall, so neither a lab decoy nor a TLS-intercepting egress can stand in for a real site) |
| 6 | node is not an open proxy (8.8.8.8:53 blocked) | lab S17, run: xray/reality (against the lab's own decoy site), hysteria2/udp, awg/userspace and direct/native, each probed from outside (TCP and UDP listeners) and from inside with a client built from the hub's credentials; 0 packets left the node; unit per Forward backend |
| 6 | Hysteria2 skipped with UDP closed, yellow event | lab S16 |
| 7 | WireGuard kernel and AWG userspace carry TCP and UDP | lab S33, run for awg/userspace (TCP 8443 and UDP 27015); wireguard/kernel: **pending** — the kernel of the lab host has no WireGuard module, S33 runs it where the kernel has one (CI); unit `internal/backend/wireguard` |
| 7 | deleting a tunnel leaves no nft rules or interfaces | lab S33, run for awg/userspace (`nft list ruleset` and `ip link` of hub and node before the tunnel and after `tunnel delete`); unit `internal/daemon/hub` |
| 8 | hub update without packet loss | lab S19, run (0 of 67 requests through the Xray client failed while hub and node were updated) |
| 8 | restore on a new server, nodes reconnect without join | lab S22, run |
| 8 | `uninstall` restores sysctl, nftables, files | lab S23, run |
| 8 | doctor diagnoses an offline node | unit `internal/doctor` |
| 9 | Persian and English documentation | `docs/fa/`, `docs/en/` |
| 9 | 5-minute video | **pending** |
| 9 | test matrix of section 17 green | CI `integration` (S30 is manual by nature) |

## Section 18 — delivery checklist

| Item | Evidence |
| --- | --- |
| 30 scenarios green, CI log attached | CI `integration`; S30 manual (real domain) |
| unit coverage ≥ 70 %, `go test -race` clean | CI `unit` (`make cover` enforces the floor) |
| one-line install on Ubuntu 22.04/24.04/26.04, Debian 12/13 (video) | lab S01 on each distro image in CI; video **pending** |
| real user reaches UP in < 5 minutes (video) | **pending** |
| resource budget of section 12 measured | [benchmarks](benchmarks.md): binary size and S25 measured; the rest on 1 vCPU / 1 GB servers **pending** |
| failover ≤ 35 s p95, switch outage ≤ 3 s with real numbers | lab S08, S32 ([lab numbers](benchmarks.md#lab-results-2026-10-01)); real-server numbers **pending** |
| no secret in logs/doctor, secrets 0600, `security audit` clean | lab S26, S01 (modes); unit `internal/daemon/hub` audit |
| node not an open proxy for every Forward backend | lab S17 (every Forward transport but wireguard/kernel, which needs the kernel module, and direct/haproxy, which runs nothing on the node) |
| `uninstall` restores the system | lab S23 |
| every DEY code in `docs/ERRORS.md` with Why/Fix | unit `internal/errors` (CI rejects codes without Why/Fix) |
| fa/en docs: install, join, first tunnel, backup node, troubleshooting, filtering FAQ, hub move, backup | `docs/fa/`, `docs/en/` |
| `docs/backends/<name>.md` per backend with pinned version and README link | `docs/backends/` |
| signed release (minisign), `SHA256SUMS`, `install.sh`, `backends.yaml` on mirror and GitHub | `release-edge` (GitHub); owner mirror **pending** (QUESTIONS.md A) |
| `CHANGELOG.md` complete, `QUESTIONS.md` answers | repository root |
| independent security review | multi-agent reviews of the control API, join, firewall and renderers (commit history); a human second reviewer **pending** |
| 72-hour test on real servers | **pending** |
