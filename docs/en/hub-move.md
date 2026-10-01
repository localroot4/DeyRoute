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
This server's public IP is 7.7.7.7, but the backup's hub address is 5.6.7.8. Use this server's address (the hub moved here)? [Y/n]
```

Answer yes. The confirmation lists what will be replaced — type `yes`. The
hub certificate is re-issued for the new address with the same CA, the
service starts and re-creates every tunnel. The end of the output tells you
the next step:

```text
Hub ir-1 restored.
If the hub moved to this server, tell the nodes the new address:
  on the old hub:   deyroute hub announce-move 7.7.7.7:44433
  or on each node:  deyroute node set-hub 7.7.7.7:44433
```

**4. On the old hub — tell the nodes.** While the old hub still runs and the
nodes are connected to it:

```bash
deyroute hub announce-move 7.7.7.7:44433
```

```text
New hub address 7.7.7.7:44433 sent to: de-1, nl-1
```

Each online node saves the new address and reconnects to the new hub. Nodes
that were offline are listed (`Offline, not told: …`); run on each of them:

```bash
deyroute node set-hub 7.7.7.7:44433
```

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

Skip step 4 and run `deyroute node set-hub NEW_IP:44433` on every node. It
works even when the node agent is stopped (the address is written to the
node's `config.yaml` and used at the next start). You still need a backup of
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

3. Check with `deyroute node list`, then give users the new IP.

## Nodes and the control port

The address you announce is `IP:CONTROL_PORT` of the new hub. The restored
configuration keeps the control port of the old hub (44433 by default); make
sure it is free on the new server and allowed in its provider firewall.

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
