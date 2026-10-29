# Moving the hub

[فارسی](../fa/hub-move.md) · [Index](index.md)

Move the hub when you change server or provider, or when the hub's IP is
blocked and you get a new one. The nodes do **not** need to join again: the
hub's internal CA is part of the backup, so the nodes keep trusting the
restored hub; they only need its new address.

## Move to a new server

**1. On the old hub — take a backup.**

```bash
deyroute backup
```

Enter a passphrase twice and note the file name it prints, for example
`/var/lib/deyroute/backups/deyroute-backup-20260930T120000Z.tar.gz.age`
([Backup & restore](backup.md)). Copy it to the new server:

```bash
scp /var/lib/deyroute/backups/deyroute-backup-20260930T120000Z.tar.gz.age root@NEW_IP:/root/
```

**2. On the new server — install without setup.**

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) --no-setup
```

(If GitHub is blocked there, use `--mirror` or `--local`; see
[Install](install.md).)

**3. On the new server — restore.**

```bash
deyroute restore /root/deyroute-backup-20260930T120000Z.tar.gz.age
```

It asks for the passphrase, checks the backup, and notices that this server
has another public IP:

```text
This server's public IP is 7.7.7.7, but the backup's hub address is 5.6.7.8. Use this server's address (the hub moved here)?
   Type y (yes) or n (no) and press Enter. Enter alone = y (yes).
```

Answer yes. The confirmation lists what will be replaced and that the hub
address changes — type `yes`. The hub certificate is re-issued for the new
address with the same CA, the service starts and re-creates every tunnel.
The end of the output tells you the next step:

```text
Hub ir-1 restored.
If the hub moved to this server, tell the nodes the new address:
  on the old hub:   deyroute hub announce-move 7.7.7.7:44433
  or on each node:  deyroute node set-hub 7.7.7.7:44433
```

In the menu: `10) Backup & Restore` → `2) Restore from a backup` asks the
same question (`Use this server's address (the hub moved here)? (y/n) [y]`),
and its result names the menu items of step 4.

The new server sits behind NAT, or its detection fails (a private address
such as `10.0.0.5`)? Then the restore cannot see the address the nodes must
dial. On a terminal (and in the menu) it asks for it, with the backup's
address as default. In a script, give it:

```bash
DEYROUTE_BACKUP_PASSPHRASE='…' deyroute restore /root/deyroute-backup-20260930T120000Z.tar.gz.age --yes --public-ip 7.7.7.7
```

Add `--public-ip6` for an IPv6 address; without it the backup's
`hub.public_ip6` is cleared. Without `--public-ip`, a restore with `--yes`
(or without a terminal) stops with `DEY-I025` and changes nothing.
`--keep-address` keeps the backup's address instead (only when this server
really owns it, for example a floating IP moved with it). The next step
printed at the end always names the address in effect.

**4. On the old hub — tell the nodes.** While the old hub still runs and the
nodes are connected to it:

```bash
deyroute hub announce-move 7.7.7.7:44433
```

```text
New hub address 7.7.7.7:44433 sent to: de-1, nl-1
```

In the menu: `10) Backup & Restore` → `3) Announce hub move`, type
`7.7.7.7:44433` and press Enter to confirm.

Each online node saves the new address and reconnects to the new hub. Nodes
that were offline are listed (`Offline, not told: …`); run on each of them:

```bash
deyroute node set-hub 7.7.7.7:44433
```

or, in the node's menu, `10) Backup & Restore` → `3) Set hub address`.
Always use the new hub's `hub.public_ip`; when it is IPv4, never the hub's
IPv6 address ([why](#nodes-and-the-control-port)).

**5. On the new hub — check.**

```bash
deyroute node list
deyroute status
```

**6. Give your users the new IP.** Their configs point to the hub's address.
If they use a domain name, change its DNS record to the new IP.

**7. Retire the old hub.** When the new hub works:

```bash
deyroute uninstall
```

Answer **no** to `Also uninstall deyroute from every online node?` and never use
`--nodes` here — the nodes now belong to the new hub.

### If the old hub is already dead

Skip step 4 and run `deyroute node set-hub NEW_IP:44433` on every node (menu:
`10) Backup & Restore` → `3) Set hub address`). It works even when the node
agent is stopped (the address is written to the node's `config.yaml` and
used at the next start). You still need a backup of
the old hub: without it, see "No backup" below.

## The hub keeps its server but gets a new IP

1. Put the new address into the hub's configuration:

   ```bash
   deyroute config edit
   ```

   change `hub.public_ip` under `hub:`, save; the file is validated and
   applied.
2. The nodes still try the old address. On every node:

   ```bash
   deyroute node set-hub NEW_IP:44433
   ```

   (menu: `10) Backup & Restore` → `3) Set hub address`). `NEW_IP` is the
   address you put in `hub.public_ip`, not another address of the hub.

3. Check with `deyroute node list`, then give users the new IP.

## Nodes and the control port

The address you announce is `IP:CONTROL_PORT` of the new hub. The restored
configuration keeps the control port of the old hub (44433 by default); make
sure it is free on the new server and allowed in its provider firewall.

Use the IP in the new hub's `hub.public_ip` for `announce-move` and
`set-hub`. When it is IPv4, never give the nodes the hub's IPv6 address:
IPv6 between hub and node is not supported, and the hub firewall then drops
the transports in which the node dials the hub. See
[Join](join.md#do-not-connect-a-node-over-ipv6).

## Versions after a move

The new hub runs the version you installed on it, which can differ from
the backup's. The restore notes the backup's version; at its first start
the hub then tells every node to install the hub's version when it
connects. A node of another major.minor (incompatible, `DEY-N004`) gets
the hub's version as soon as it connects anyway. Until it runs it, an
incompatible node runs no commands. You need no `deyroute update` for
this; watch it with `deyroute node list` (`VERSION`). A node newer than
the new hub goes back to the hub's version. See
[How the nodes follow](update-uninstall.md#how-the-nodes-follow).

## No backup: join the nodes again

Without a backup the old CA is gone, so the nodes cannot trust a new hub.
Start fresh:

1. Set up the new hub normally ([Install](install.md)).
2. On every node remove deyroute and join the new hub:

   ```bash
   deyroute uninstall --yes
   ```

   then run a new join command from the new hub
   (`deyroute node join-command`, one per node).
3. Create the tunnels again ([First tunnel](first-tunnel.md)).

`deyroute uninstall` on a node removes only DEYROUTE; your VPN service on the
node is not touched.

Take a backup after every change from now on — it turns a hub move into a
five-minute job.
