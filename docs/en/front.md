# Through Cloudflare (front mode)

Sometimes the direct path between the hub (Iran) and a node (abroad) is
filtered. Typically a connection opens, then is cut after a few exchanges.
No direct tunnel can work between those two addresses then.

Front mode puts the hub behind a Cloudflare record. The node connects to
Cloudflare instead of the hub's IP, and Cloudflare carries the connection to
the hub:

- the control channel goes through Cloudflare (status, commands, updates);
- **the tunnel traffic** goes through Cloudflare too (what your users
  exchange);
- your users see no change: they still connect to the hub's IP and port.

```
user ──► hub (Iran) ◄── Cloudflare ◄── node (abroad) ──► VPN service
```

Cloudflare only sees ciphertext. Inside the connection runs a separate TLS
layer that only the hub and the node can open.

## What you need

- a domain on Cloudflare (the free plan is enough);
- version 0.3.0 or newer on both servers (`deyroute update`).

## 1. Cloudflare (once)

1. **DNS**: an `A` record, e.g. `t1`, pointing to **the hub's IP**, with the
   orange cloud (**Proxied**) on.
2. **SSL/TLS → Overview**: mode **Full**.
3. **Network**: **WebSockets** on.

## 2. On the hub

```bash
deyroute front enable --domain t1.example.com
```

Replace `t1.example.com` with your record. This turns the front on, on port
`2053` (a port Cloudflare proxies), and ends with something like:

```
Cloudflare front: on · t1.example.com:2053 · listening · tls auto
...
On each node that should go through Cloudflare, run (keep it private, it holds the front's secret path):
   deyroute node set-hub 'wss://t1.example.com:2053/...'
```

Show it again any time:

```bash
deyroute front status
```

## 3. On the node

Run the line the hub printed:

```bash
deyroute node set-hub 'wss://t1.example.com:2053/...'
```

The node connects through Cloudflare at once, and its tunnels are rebuilt to
go through it. Nothing else is needed: no new tunnel, no new join.

**New nodes:** while the front is on, `deyroute node join-command` gives a
link whose join itself goes through Cloudflare.

## 4. Check

On the hub:

```bash
deyroute status
```

- the node shows `via front`;
- the tunnel is `UP`.

## Which transports go through Cloudflare

Only transports in which the node dials the hub over TCP:

- Backhaul (`tcp`, `tcpmux`, `ws`, `wsmux`, `wssmux`)
- rathole
- FRP (`tcp`, `websocket`)
- chisel and gost

For a node that uses the front, the failover engine skips the others with
`DEY-B012` and takes the next rung:

- transports that need UDP (Hysteria2, WireGuard, `backhaul/udp`);
- transports in which the hub dials the node (`xray/reality`,
  `direct/native`);
- Waterwall, because it checks the node's real address;
- in a tunnel with both TCP and UDP maps, transports that need a second
  process for the UDP maps.

## Speed and stability

- **Speed:** latency grows a little, since every packet passes a Cloudflare
  data centre. The free plan is fine for a VPN.
- **Cloudflare drops:** when Cloudflare drops a connection, the node
  reconnects by itself, and the tunnel comes back up on its own.
- **Firewall:** the hub opens the front port to Cloudflare's addresses only
  (`hub.front.cf_only`). Anyone else who opens it sees an ordinary web page.
  The secret path only keeps scanners out; security comes from the inner TLS.

## Back to direct

When the direct path works again, on the node:

```bash
deyroute node set-hub HUB_IP:44433
```

To turn the front off on the hub:

```bash
deyroute front disable
```

It names the nodes that used the front, with the command that puts each
back on the direct address.

## Another port

The default port is `2053`. You can pick any port Cloudflare proxies:

| Kind | Ports | SSL/TLS mode in Cloudflare |
| --- | --- | --- |
| HTTPS | `2053`, `2083`, `2087`, `2096`, `8443` | **Full** |
| HTTP | `8080`, `8880`, `2052`, `2082`, `2086`, `2095` | (any) |

```bash
deyroute front enable --domain t1.example.com --port 8443
```

Keep 443 for your tunnels.

## Common errors

| Code | Meaning | Fix |
| --- | --- | --- |
| `DEY-N016` | the node cannot reach Cloudflare (DNS, TLS, clock) | check the DNS record and the orange cloud; the node's clock must be right |
| `DEY-N017` with 522 | Cloudflare cannot reach the hub | the record must point to the hub's IP; `deyroute front status` must say `listening`; the port must be open in the hub provider's panel |
| `DEY-N017` with 400/426 | WebSockets are off in Cloudflare | turn on Network → WebSockets |
| `DEY-N017` with 525/526 | wrong SSL/TLS mode | use **Full** for the HTTPS ports |
| `DEY-N018` | the hub refused a tunnel connection | restart the tunnel: `deyroute tunnel restart <tunnel>`; the two clocks must not differ by more than 2 minutes |
| `DEY-B012` | this rung cannot run through Cloudflare | nothing to do; the next rung is used |
