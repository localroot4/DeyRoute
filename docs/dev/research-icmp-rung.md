# Research: an ICMP or raw-socket rung (DR-22)

Status: research only. No code, no rung, no change to the default ladder.
Nothing below has been run against DEYROUTE yet; every number is a claim
of the upstream project until the spike in [Next step](#next-step) measures it.

## Question

A field report (net4people BBS #676, not verified by us) says that on some
Iran ↔ EU routes only ICMP gets through while TCP and UDP to the node are
cut. Can DEYROUTE add a rung that carries the tunnel inside ICMP echo
packets, or inside raw-socket packets that look like another protocol, and
what would it cost?

## What a rung must fit

A rung is one backend transport in the ladder (spec section 7). For a new
one the code already fixes these constraints:

| Constraint | Where | Effect on an ICMP rung |
| --- | --- | --- |
| The backend is a pinned binary from the signed manifest | `backends.yaml`, `internal/daemon/node/fetch.go` (DR-03) | upstream must publish linux amd64 and arm64 builds with a stable URL; we pin their sha256 |
| Units run as user `deyroute` with only `CAP_NET_BIND_SERVICE`, unless the allowlist grants more | `internal/daemon/render/privileges.go` (DR-04) | ICMP needs a raw socket: `CAP_NET_RAW` and address family `AF_INET`/`AF_INET6` raw. The allowlist entry is new, reviewed code |
| No backend may relax `NoNewPrivileges` or the bounding set | same | fine: `CAP_NET_RAW` as an ambient capability is enough |
| The health probe goes end to end through the echo port | DR-17 | fine for a rung that carries TCP |
| The node's firewall is scoped to the hub (`@nodes`, DR-18 style) | `internal/firewall` | the node must accept ICMP echo-request only from `hub.public_ip` for this rung |
| Direction | forward and reverse rungs exist | ICMP is request/reply: the side that sends echo-request is the client. The hub (Iran) is the client, the node the server, so the node answers only the hub |

## Candidates

| Project | Carries | Language | License | Notes |
| --- | --- | --- | --- | --- |
| [pingtunnel](https://github.com/esrrhs/pingtunnel) | TCP, UDP, SOCKS5 over ICMP echo | Go | MIT per Nixpkgs metadata, **to verify** in the repo | the most active ICMP tunnel; releases for linux amd64/arm64; the README asks to turn off the kernel's own ping replies on the server |
| [udp2raw](https://github.com/wangyu-/udp2raw) | UDP as fake TCP, UDP or ICMP | C++ | **to verify** | raw socket plus BPF; would carry a UDP rung (hysteria2, WireGuard) inside ICMP |
| [phantun](https://github.com/dndx/phantun) | UDP as fake TCP | Rust | Apache-2.0 or MIT per its README, **to verify** in the upstream repo | TUN based, multi-core; no ICMP mode, so it answers "UDP blocked, TCP open", not "only ICMP" |

hans and ptunnel-ng are older ICMP tunnels; pingtunnel covers the same
ground with a maintained Go code base, so they are not considered further.

## Problems to solve before a rung

1. **The kernel answers pings too.** On the node, every echo-request from
   the hub also gets a reply from the kernel. The client then sees two
   replies per request. pingtunnel's advice is
   `net.ipv4.icmp_echo_ignore_all=1`, which turns off ping for the whole
   server and breaks the owner's monitoring. A scoped alternative is an nft
   rule in output that drops echo-replies to the hub that no socket sent.
   Whether nft can tell a raw-socket reply from a kernel reply (`meta
   skuid`, socket mark) must be tested; until then this is open.
2. **Capability.** `CAP_NET_RAW` lets the process read every ICMP packet the
   host receives and send arbitrary IP packets of that protocol. That is
   more than any rung has today; the allowlist entry needs a security review
   (`security.md`).
3. **Throughput and MTU.** One echo packet carries at most the path MTU minus
   28 bytes (IPv4 + ICMP header) minus the tool's own header. Every segment
   needs a reply, and many networks rate-limit ICMP. Expect a few Mbit/s,
   enough for messaging, not for video. To measure.
4. **Detectability.** Echo-requests with large, high-entropy payloads at a
   steady rate are easy to spot and to throttle. A filter that leaves ICMP
   open today can rate-limit it tomorrow; the rung would be a last resort,
   never a default.
5. **Probing.** The ladder decides on probes through the tunnel. An ICMP
   rung with a high loss rate will flap; its `fail_threshold` likely needs
   to be higher than the default.
6. **IPv6.** ICMPv6 echo works the same way, but the tools' IPv6 support
   differs; to check per tool.

## Recommendation

- Do not add an ICMP rung to the default ladders.
- If a spike confirms points 1–3, add pingtunnel as an **opt-in** rung
  (`pingtunnel/icmp`, last in a custom ladder), with its own allowlist entry
  (`CAP_NET_RAW`, raw `AF_INET`) and a node firewall rule that accepts
  echo-request only from `hub.public_ip`.
- udp2raw's ICMP mode is the fallback if a UDP rung inside ICMP measures
  better than pingtunnel's TCP.
- phantun is a separate idea (UDP blocked, TCP open) and needs its own
  report showing that case exists.

## Next step

A spike outside `main`, in the integration lab:

1. A lab block that drops every TCP and UDP between hub and node but lets
   ICMP pass (`lib.sh` `block`, new mode).
2. pingtunnel server on the node, client on the hub, by hand as user
   `deyroute` with only `CAP_NET_RAW`.
3. Measure: setup time, throughput of a 10 MB download, RTT added, loss at
   1, 5 and 10 Mbit/s, CPU on both sides.
4. Test the scoped kernel-reply drop (problem 1) in nft.
5. Write the numbers into this file and decide on the opt-in rung.

Real-server numbers (DR-25) need the owner's servers on an affected route.
