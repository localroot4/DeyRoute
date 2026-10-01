# Join a node

[فارسی](../fa/join.md) · [Index](index.md)

A node is a foreign server that runs your real VPN service. Joining connects
it to the hub once; after that the node keeps an encrypted control connection
to the hub and does whatever the hub asks (install a backend, start or stop a
transport, run a probe). The hub never connects to the node for control and
SSH is not used anywhere.

## 1. Get a join command on the hub

```bash
deyroute node join-command
```

The command line prints:

```text
Run this command on the new node (one node per command, valid until 12:15, 15m):

bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) join 'dey://TOKEN@5.6.7.8:44433#sha256:…' --version 1.0.0
```

In the menu, `3) Nodes` → `1) Show join command` shows the same line on a
plain screen, so that it can be copied whole (Enter returns to the menu):

```text
 Run this one line on the new node (as root):

bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) join 'dey://TOKEN@5.6.7.8:44433#sha256:…' --version 1.0.0

 Single use; expires at 12:15:00 (in 15 minutes). Press r for a new command.

 Copy the whole line above, then press Enter to return to the menu.
```

Back in the menu, `r` makes a new command.

- The link inside is `dey://TOKEN@HUB_IP:CONTROL_PORT#CA_FINGERPRINT`. Copy
  the whole line; do not edit it.
- A join command is **single use**: one command joins one node. Make a new
  command for every node.
- It is valid for 15 minutes. For a longer window use `--ttl`, from `1m` to
  `24h`: `deyroute node join-command --ttl 1h`.
- `--version` makes the node install the same release as the hub. Without
  it the installer would take the newest release, and a hub on an older
  major.minor could not use the node (`DEY-N004`).
- When the hub was set up with `DEYROUTE_MIRROR`, the command downloads the
  installer from that mirror.

## 2. Run it on the node

On the foreign server, as root, paste the line. To choose the node's id, add
`--name` at the end:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) join 'dey://TOKEN@5.6.7.8:44433#sha256:…' --version 1.0.0 --name de-1
```

If `deyroute` is already installed on the node, the short form works too:

```bash
deyroute join 'dey://TOKEN@5.6.7.8:44433#sha256:…' --name de-1
```

The node asks one question — whether to apply the balanced kernel profile
(Enter = yes) — and then shows its steps:

```text
  ✔ Read join link
  ✔ Create node key
  ✔ Join hub
  ✔ Save certificates
  ✔ Apply kernel settings
  ✔ Write /etc/deyroute/config.yaml
  ✔ Install and start service

Node de-1 joined hub ir-1; it appears on the hub dashboard shortly
```

The node id comes from `--name` (letters, digits and `-`, 2–32 characters,
e.g. `de-1`) or, without it, from the host name. The id never changes later;
the display name can: `deyroute node rename de-1 "Germany 1"`.

## 3. Check it on the hub

```bash
deyroute node list
deyroute node test de-1
```

`node list` shows every node with its state, control RTT, version and load,
and below the table the last error each node agent reported (for example
`Last error on de-1: DEY-B003 …`; the menu's `3) Nodes` → `2) List` shows it
as `Last error`). `node test` measures the control RTT, tests UDP between hub and node
(`UDP ok` or `blocked`: transports that need UDP are then skipped for this
node) and prints the node's system: OS, kernel, CPUs, `Memory: 3.8 GiB`,
`Uptime: 10d 00:02`, the deyroute version (the menu's `3) Nodes` → `5) Test`
shows the same). The dashboard (`deyroute status`, or `1) Dashboard (live)` in
the menu) shows the node under NODES.

Next step: [your first tunnel](first-tunnel.md).

## What happens during a join

1. The node creates its own key pair (Ed25519) and sends a certificate
   request with the token to the hub's control port.
2. The node checks the hub's CA against the fingerprint in the link (it
   trusts nothing else). A different CA stops the join with `DEY-N002`.
3. The hub checks the token (32 random bytes, single use, deleted when used),
   signs a node certificate with its internal CA (valid 10 years) and adds
   the node's IP address to the firewall set `@nodes`.
4. The node writes its certificate and `config.yaml`, starts
   `deyroute-node.service` and keeps one outbound connection to the hub
   (reconnecting with a 1–30 s backoff). It sends a heartbeat every 5 seconds;
   the hub marks it offline after 15 seconds without one.

The control port normally accepts only known nodes. While an unused join
token exists, it is open to every address so that a new node can reach it.
More than 5 failed join attempts in an hour from one address block that
address for an hour (`DEY-N007`).

## When the node's IP changes

Nothing to do. The node reconnects from its new address with its
certificate; the hub accepts it, puts the new IP into `@nodes` and records a
`node_ip_changed` event (`deyroute events`).

When the **hub's** address changes, the nodes must be told: see
[Hub move](hub-move.md).

## Incompatible versions

Hub and node must run the same major.minor version (1.0.x with 1.0.x). A node
with another version connects, is shown as `(incompatible)` in
`deyroute node list`, and runs no commands until it is updated (`DEY-N004`).
The join command already pins the hub's version, so this normally happens
only after updating one side. To fix it, bring both to the same release:

- on the hub: `deyroute update` (the nodes follow the hub), or
- on the node: run the installer again with the hub's version (it upgrades in
  place, the configuration stays):

  ```bash
  bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) --version 1.0.0
  ```

`deyroute version` prints the version on each server.

## Remove or re-join a node

```bash
deyroute node remove nl-1
```

This stops every tunnel transport on the node (its tunnels continue on their
other nodes), revokes its certificate and removes its IP from the firewall. A
tunnel whose only node it is must get another node first
(`deyroute tunnel backup add <tunnel> --node <id>`) or be deleted; otherwise the
removal is refused with `DEY-C008`. It
asks you to type `yes` (`--yes` in scripts). The node server itself is not
cleaned: run `deyroute uninstall` on it to remove deyroute there.

To join a server again (for example to another hub), run
`deyroute uninstall` on it first, then a new join command. If the hub already
has a node with the same id, remove it on the hub first or pick another id
with `--name` (`DEY-N010`).

## Join errors

| Code | Meaning | What to do |
| --- | --- | --- |
| `DEY-N001` | token invalid or expired | create a new command: `deyroute node join-command` |
| `DEY-N002` | hub CA fingerprint mismatch | copy the command again from the hub; do not edit it; check the address |
| `DEY-N006` | the link is incomplete | copy the full line again |
| `DEY-N007` | too many failed attempts from this IP | wait one hour, then use a new command |
| `DEY-N009` | the hub cannot be reached | check that the hub runs and that the node can reach `HUB_IP:44433` (provider firewall) |
| `DEY-N010` | a node with this id exists | `--name` with another id, or `deyroute node remove <id>` on the hub |
| `DEY-N020` | the hub's answer cannot be used | same version on both servers, then a new join command |
| `DEY-I013` | this server is already set up | `deyroute uninstall` first |

More codes: [Troubleshooting](troubleshooting.md) and [ERRORS.md](../ERRORS.md).
