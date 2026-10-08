# Automatic tuning

[فارسی](../fa/tuning.md) · [Index](index.md)

`deyroute optimize auto` measures every server (the hub and each online
node) and sets the kernel and service limits that suit it. Nothing changes
silently: every change is listed with its current value, its new value, when
it takes effect and why, and it is applied only after one confirmation.
Everything is undone with `deyroute optimize revert`.

In the menu: `7) Optimize → 1) Automatic tuning (recommended)`. The setup
wizard offers the same as its last question (*Tune this server
automatically*), with the list of this server's changes shown first.

## Commands

| Command | What it does |
| --- | --- |
| `deyroute optimize auto --dry-run` | show the plan of every server; change nothing |
| `deyroute optimize auto` | show the plan, ask once (type `yes`), apply it |
| `deyroute optimize auto --yes` | the same without the question (scripts) |
| `deyroute optimize auto --backends` | also size the transports to each server (restarts the active transport of the tunnels it lists) |
| `deyroute optimize apply --profile auto` | the same as `optimize auto --yes` |
| `deyroute optimize check` | compare the tuned values with the live kernel; drift exits with code 2 (`DEY-X067`) |
| `deyroute optimize status` | profile of the hub and of every node, and which nodes still have to apply it |
| `deyroute optimize revert` | restore everything from before deyroute |

Without a terminal and without `--yes`, `optimize auto` prints the plan and
what it would do, changes nothing and exits with code 3. `--json` prints the
plan (`--dry-run`) or the result with its steps (see
[the JSON reference](../cli-json.md)).

The plan looks like this:

```text
Automatic tuning plan

hub · 2.0 GiB RAM · 2 CPU · kernel 6.1.0-21-amd64 · eth0 MTU 1500 · qdisc fq_codel
  KEY                               NOW       NEW                EFFECT        WHY
  net.core.default_qdisc            fq_codel  fq                 after reboot  fq paces every flow; it applies to network interfaces created from now on …
  net.ipv4.tcp_congestion_control   cubic     bbr                now           BBR keeps throughput high on long and lossy paths (new connections)
  net.core.rmem_max                 212992    33554432           now           socket buffers up to 32 MiB, sized for 2 GiB of RAM
  net.ipv4.ip_local_reserved_ports  -         30000-31999,44433  now           keeps 30000-31999,44433 free for deyroute's listeners …
  …

node de-1 · 1.0 GiB RAM · 1 CPU · kernel 5.15.0 · container: lxc
  nothing to change
  skipped net.core.rmem_max, net.core.wmem_max, …: DEY-X064 the kernel belongs to the machine this lxc container runs on

node nl-1
  offline: it applies the plan when it reconnects

14 changes on 3 servers.
Undo any time with: deyroute optimize revert
```

**EFFECT** says when a change is in force: `now`, `after reboot`, `next
start` (the service picks it up the next time it starts; nothing is
restarted for it) or `restarts tunnels` (only with `--backends`).

Running `optimize auto` again lists nothing when nothing changed: the plan
is idempotent. The plan has a hash; if anything changes between the list and
your `yes` (a node comes online, someone edits a value), the apply is
refused with `DEY-X065` and you see the new list instead.

## Seeing the settings in effect

`deyroute optimize status` shows the settings in groups, one line each with
what the group means now ("BBR · fq", "up to 32 MiB per connection"); every
value with `--details`. In the menu: `7) Optimize` → `6) Settings in
effect`; choosing a group opens what it is for and every value in it.

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="../assets/screens/tui-settings-dark.svg">
    <img src="../assets/screens/tui-settings-light.svg" alt="The Settings in effect menu: the groups of settings, each with its summary, such as BBR · fq and up to 32 MiB per connection.">
  </picture>
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="../assets/screens/tui-settings-group-dark.svg">
    <img src="../assets/screens/tui-settings-group-light.svg" alt="The Buffers group opened: a short explanation, the green summary and every value, such as rmem_max 32 MiB.">
  </picture>
</p>


## What is measured

Only files under `/proc` and `/sys` are read (and the kernel is asked for the
network card's queue); no program is started:

- RAM and CPUs (a CPU quota of the service's cgroup counts);
- the kernel release, whether BBR and the `fq` queue are available, and the
  queue of the default-route network card, its MTU and speed;
- whether the server is a container (OpenVZ, LXC, Docker);
- the conntrack table: loaded, its size and how full it is;
- the current value of every key the plan would change.

## What changes, and why it is safe

The base is the `balanced` profile (the same keys and values), with a layer
computed from the facts:

| Item | Value | Why it is safe |
| --- | --- | --- |
| socket buffers (`rmem_max`, `wmem_max`, the third value of `tcp_rmem`/`tcp_wmem`) | 16 MiB below 2 GB RAM, 32 MiB below 4 GB, 64 MiB from 4 GB | they are maximums: the kernel grows a buffer only for a connection that needs it |
| limit keys (`somaxconn`, `tcp_max_syn_backlog`, `netdev_max_backlog`, `fs.file-max`, `fs.nr_open`, buffer maximums) | raise only | a value that is already higher on the server is never lowered; it is listed as left alone and not touched by a revert either |
| `tcp_slow_start_after_idle = 0` | | tunnels keep long-lived connections; an idle one does not start slow again |
| `tcp_notsent_lowat = 16384` | with BBR | keeps queued data small, so BBR measures the path well |
| `rmem_default`/`wmem_default = 1 MiB` | when Hysteria2 or AmneziaWG rungs exist | UDP transports read the default buffer |
| `fs.nr_open ≥ 1048576` | | the services start with `LimitNOFILE=1048576`, which systemd refuses above `fs.nr_open` |
| `ip_local_reserved_ports` | `30000-31999`, the control port (and the front port) | outgoing connections never take a port deyroute needs; existing entries are kept |
| `default_qdisc = fq` | when the kernel has `sch_fq` | see [The fq queue](#the-fq-queue-needs-a-reboot) |
| `tcp_congestion_control = bbr` | when the kernel has BBR and `tuning.bbr` is on | new connections only; otherwise the item is skipped with the reason |
| conntrack (below) | when conntrack is loaded or the firewall needs it | |

Besides `/etc/sysctl.d/99-deyroute.conf` the plan may write:

- `/sys/module/nf_conntrack/parameters/hashsize` and
  `/etc/modprobe.d/deyroute.conf` (the conntrack hash table, now and at
  boot) and `/etc/modules-load.d/deyroute.conf` (loads `nf_conntrack` at
  boot, so its keys apply);
- service limits in drop-in files `60-deyroute-auto.conf` next to the
  deyroute units: the transports get `OOMScoreAdjust=300` (on a small server
  a runaway transport is stopped first, never sshd or the hub; systemd and
  the failover restart it), all transports together get a memory bound of
  75 % of RAM (`MemoryHigh`, the kernel reclaims, nothing is killed by it),
  and the hub gets `GOMEMLIMIT` by RAM (128, 256 or 512 MiB). A
  `daemon-reload` reads them; nothing is restarted, they apply at the next
  start.

What is **never** touched: SSH, the firewall tables of other programs,
Docker, and other files in `/etc/sysctl.d` (they are only read, to report
overrides). The firewall table `inet deyroute` already clamps the TCP MSS on
WireGuard paths; automatic tuning lists it but does not change it.

### Transports (`--backends`)

With `--backends` each server gets a sticky size class (`small` below 1 GiB
RAM or with one CPU, `medium` below 4 GiB, `large` above), stored in
`config.yaml`. It scales the Backhaul mux buffers, the frp connection pool
(unless `advanced.connection_pool` is set) and the Waterwall memory profile,
and sets the WireGuard MTU to fit a network card below 1500. These items
rewrite the transport's files and restart the **active transport** of the
tunnels listed (users reconnect once), so they are never part of the plan
without the flag.

## Conntrack

Connection tracking keeps one entry per flow. The kernel default size is
small on a 512 MB–1 GB VPS, and a busy hub with many mobile and QUIC users
can fill it: the kernel then logs `nf_conntrack: table full, dropping
packet` and new connections fail. The plan sets:

- `nf_conntrack_max` = 64 entries per MiB of RAM, between 65536 and
  1048576 (raise only);
- the hash table to a quarter of it;
- `nf_conntrack_tcp_timeout_established = 86400` (1 day instead of 5):
  WireGuard keepalives (25 s) keep live tunnel flows open, so only dead
  entries expire sooner. A value you set yourself is kept (the item is
  skipped).

`deyroute optimize check` reports a table that is more than 80 % full.

## The fq queue needs a reboot

`default_qdisc = fq` applies to network cards created from now on. The card
that is already up keeps its queue until the next boot: replacing it at
runtime would drop packets, so deyroute never does it. The plan shows this
item as `after reboot`. BBR works without `fq` on kernel 4.13 and later (it
paces by itself), so nothing is lost until then.

## Containers

In OpenVZ, LXC or Docker the kernel belongs to the host: `/proc/sys` is
read-only or shared. Every kernel item is skipped with `DEY-X064` and only
the service limits apply. Ask the provider for a KVM or bare-metal server if
you want the kernel tuned.

## Nodes

`optimize auto` plans and applies on every online node too; an offline node
is shown as `pending` and applies the plan when it reconnects. Your `yes`
is also the consent for nodes that join later: the hub tunes them the same
way when they come online. A node whose agent is too old for automatic
tuning gets `balanced` and a warning (update it with `deyroute update`).
`deyroute optimize status` shows each node's profile and whether it is still
pending.

## When someone else changes a value

`deyroute optimize check` (menu `7) Optimize → 5) Check tuning`) compares
every value deyroute set with the live kernel. A difference is listed with
who changed it:

- a file that sorts after `99-deyroute.conf` in `/etc/sysctl.d`,
  `/run/sysctl.d` or `/usr/lib/sysctl.d`, or **`/etc/sysctl.conf`**, which
  systemd applies last and so wins over every `sysctl.d` file;
- otherwise a runtime write (another tool, a script, a panel agent).

Remove the other setting, or keep it on purpose. `deyroute optimize auto`
lists the value again if you want deyroute's. The hub also runs the check
every 6 hours and records a warning event for each new difference; it never
changes anything by itself. At boot, deyroute only re-applies its own
confirmed values that the kernel did not have yet (for example conntrack
keys that appear once the module is loaded).

## Undo

`deyroute optimize revert` (menu `7) Optimize → 3) Revert`):

- restores every key to its value from before deyroute — but only when the
  live value is still the one deyroute wrote; a key someone changed since is
  left alone, with a warning;
- never lowers a limit key below what is in use (current use × 1.25): such a
  key keeps its live value, leaves the conf file and a warning says the
  lower value comes back at the next boot;
- restores the conntrack hash table, removes the module and modprobe files
  and the service drop-ins (with a `daemon-reload`);
- clears the transport size classes; the affected tunnels are listed and
  restarted;
- sets the nodes back to `off`.

The backup of the old values is deleted only when everything was restored.
`deyroute uninstall` does the same.

## Fixed profiles

The fixed profiles stay available for owners who prefer them:
`deyroute optimize apply --profile balanced|aggressive|off` (menu
`7) Optimize → 2) Apply profile`). Choosing one of them after `auto` leaves
automatic tuning: nodes are no longer tuned automatically. Automatic tuning
never changes the order of your transports or which one is active.
