# DEYROUTE error codes

<!-- Generated from internal/errors/codes.go by `make docs`. Do not edit by hand. -->

Every error is shown as three fixed lines plus the log path:

```text
✖ DEY-P012  Port 443/tcp is already in use
  Why:  nginx (pid 1234) is listening on 0.0.0.0:443
  Fix:  choose another port, or stop that service first
  Log:  /var/log/deyroute/hub.log (search DEY-P012)
```

Placeholders such as `{port}` are filled at runtime. CLI exit codes: `1` for every category except `DEY-X` (`2`); `3` means a confirmation was required but `--yes` was not given.

## Installer / environment (DEY-I0xx)

| Code | Message | Why | Fix |
| --- | --- | --- | --- |
| `DEY-I001` | This command must run as root | deyroute manages systemd units, nftables and files under /etc and /var/lib | re-run with sudo, e.g.: sudo bash install.sh |
| `DEY-I002` | systemd was not found | deyroute runs its hub, node and tunnel processes as systemd units | use a distribution with systemd 245 or newer (Ubuntu 22.04+, Debian 12+) |
| `DEY-I003` | Unsupported CPU architecture: {arch} | release binaries exist only for linux/amd64 and linux/arm64 | use an amd64 or arm64 server |
| `DEY-I004` | Download failed from every source: {file} | the mirror, the release base and GitHub were each tried 3 times without success | pass --mirror URL, set DEYROUTE_MIRROR, or download the archive elsewhere and use --local /path/file.tar.gz |
| `DEY-I005` | Checksum mismatch for {file} | the downloaded file does not match SHA256SUMS; it is corrupt or was tampered with | retry; if it persists use another mirror or --local with a file you verified |
| `DEY-I006` | Signature invalid for {file} | SHA256SUMS.minisig was not produced by the DEYROUTE release key | use the official mirror; only for testing, --skip-signature bypasses this check |
| `DEY-I007` | Neither curl nor wget is installed | the installer needs one of them to download release files | apt-get install -y curl   (or: dnf install -y curl) |
| `DEY-I008` | systemd is too old ({version}) | deyroute needs systemd 245 or newer for unit sandboxing options | upgrade the distribution to a supported release |
| `DEY-I009` | Linux kernel is too old ({version}) | deyroute needs kernel 5.4 or newer (BBR, nftables features) | upgrade the kernel or the distribution |
| `DEY-I010` | iproute2 (the ip command) is missing | public IP detection and WireGuard transports use iproute2 | apt-get install -y iproute2   (or: dnf install -y iproute) |
| `DEY-I011` | Neither nftables nor iptables is installed | deyroute manages its own firewall table and needs one of them | apt-get install -y nftables   (or: dnf install -y nftables) |
| `DEY-I012` | ca-certificates is missing | HTTPS downloads cannot be verified without the system CA bundle | apt-get install -y ca-certificates |
| `DEY-I013` | This server is already set up as {role} | /etc/deyroute/config.yaml already exists; setup never overwrites it | use the menu to change settings, or run: deyroute uninstall   then set up again |
| `DEY-I014` | Setup step failed: {step} | a required setup step could not complete | read the log below, fix the cause and run the installer again (it repairs, never reinstalls) |

## Configuration (DEY-C0xx)

| Code | Message | Why | Fix |
| --- | --- | --- | --- |
| `DEY-C001` | Unknown key in config: {key} | config.yaml has a strict schema; unknown keys are rejected (line {line}) | remove or rename the key; run: deyroute config validate |
| `DEY-C002` | Duplicate {kind} id: {id} | ids must be unique within their list | give each {kind} a different id |
| `DEY-C003` | Listen port {port} is used by two tunnels | tunnels {tunnel} and {other} both listen on {port}; only one process can bind a port | change the listen port of one tunnel |
| `DEY-C004` | Invalid target '{target}' in tunnel {tunnel} | a target must be host:port with a port between 1 and 65535 | use the form 127.0.0.1:443 |
| `DEY-C005` | Unknown transport: {transport} | the ladder contains a transport that no backend provides | use one of: {valid} |
| `DEY-C006` | Config schema migration failed ({from} -> {to}) | the config file could not be converted to the current schema | restore a backup from /var/lib/deyroute/backups/auto/ or fix the file by hand, then run: deyroute config validate |
| `DEY-C007` | Invalid {kind} id: '{id}' | ids may contain only a-z, 0-9 and '-', 2 to 32 characters | choose an id like de-1 or main |
| `DEY-C008` | Tunnel {tunnel} has no node | every tunnel needs at least one node | add a node: deyroute tunnel edit {tunnel}   or   deyroute tunnel backup add {tunnel} --node <id> |
| `DEY-C009` | Tunnel {tunnel} has an empty ladder | at least one transport is required | use the default ladder: deyroute tunnel edit {tunnel} --ladder default |
| `DEY-C010` | Tunnel {tunnel} refers to unknown node {node} | the node id is not in the nodes list | join the node first (deyroute node join-command) or fix the id |
| `DEY-C011` | Listen port {port} is reserved | {reason} | pick another port; deyroute port suggest shows free ones |
| `DEY-C012` | Unknown ladder '{ladder}' in tunnel {tunnel} | the ladder name is not defined under ladders: | use an existing ladder (deyroute ladder list) or create it |
| `DEY-C013` | Invalid value for {field}: '{value}' | allowed values: {allowed} | correct the value and run: deyroute config validate |
| `DEY-C014` | Cannot read config {path} | the file is missing, unreadable or not valid YAML | check the file, or restore the last good copy from /var/lib/deyroute/backups/auto/ |
| `DEY-C015` | Tunnel {tunnel} has {count} port maps (max 64) | a tunnel supports at most 64 port maps | use a port range (e.g. 2000-2010) or split into several tunnels |
| `DEY-C016` | Config role '{role}' does not match its sections | a hub config needs hub:, a node config needs node:, and not both | fix role: or the sections; run: deyroute config validate |
| `DEY-C017` | Could not write config {path} | the atomic write (temp file + rename) failed | check free disk space and permissions on /etc/deyroute |
| `DEY-C018` | The {kind} id '{id}' cannot be changed | ids are immutable after creation; only the name can change | change the name instead, or delete and re-create |
| `DEY-C019` | Unsupported schema_version {version} | this deyroute build does not know that schema version | update deyroute (deyroute update) or restore an older config |
| `DEY-C020` | Could not understand port input '{input}' | accepted forms: 443, 443/udp, 443,2053, 2000-2010, 443:8443 | re-enter the ports using one of the accepted forms |

## Node / control channel (DEY-N0xx)

| Code | Message | Why | Fix |
| --- | --- | --- | --- |
| `DEY-N001` | Join token is invalid or expired | tokens are single-use and expire after 15 minutes | on the hub run: deyroute node join-command   and use the new line |
| `DEY-N002` | Hub CA fingerprint mismatch | the hub presented CA {got}, the join link pins {expected}; this may be a wrong address or interception | copy the join command again from the hub menu; do not edit it |
| `DEY-N003` | Node {node} is offline | no heartbeat from the node's control connection | check the node: systemctl status deyroute-node   and its network to the hub |
| `DEY-N004` | Node {node} version {node_version} is incompatible with hub {hub_version} | hub and node must share the same major.minor version | update the node from the hub menu: Update -> deyroute (nodes follow the hub) |
| `DEY-N005` | Command {command} timed out on node {node} | the node did not answer in time | check deyroute node test {node}; see the node log with deyroute logs node |
| `DEY-N006` | Invalid join link | expected dey://TOKEN@HUB_IP:PORT#SHA256_FINGERPRINT | copy the full line again from the hub (deyroute node join-command) |
| `DEY-N007` | Too many failed join attempts from {ip} | more than 5 failures in an hour block the address for 1 hour | wait one hour, then use a fresh join command |
| `DEY-N008` | Unknown node: {node} | no node with that id has joined this hub | list nodes with: deyroute node list |
| `DEY-N009` | Cannot reach the hub at {addr} | the control connection could not be opened | check that the hub is up and that {addr} is reachable from this server (firewall, IP) |
| `DEY-N010` | A node with id {node} already joined | node ids are unique per hub | use --name to pick another id, or remove the old node on the hub first |
| `DEY-N011` | Command {command} failed on node {node} | the node reported an error while running the command | see details below and the node log: deyroute logs node |
| `DEY-N012` | No online node available to download through | the hub fetches files via a node because GitHub is often unreachable from Iran | bring a node online, or set DEYROUTE_MIRROR to a reachable mirror |

## Ports / firewall (DEY-P0xx)

| Code | Message | Why | Fix |
| --- | --- | --- | --- |
| `DEY-P010` | Invalid port: {input} | ports must be numbers between 1 and 65535 | enter a port like 443 or 443/udp |
| `DEY-P011` | Port {port} is reserved | {reason} | pick another port; deyroute port suggest shows free ones |
| `DEY-P012` | Port {port} is already in use | {process} is listening on {addr} | choose another port, or stop that service first |
| `DEY-P013` | Firewall {firewall} blocks port {port} | an external firewall rule drops traffic to this port | allow it: {command} |
| `DEY-P014` | Port {port} is not reachable from node {node} | the node could not connect to the hub's public IP on this port (datacenter firewall or routing) | open the port in your provider's firewall panel; run deyroute port check {port} again |
| `DEY-P015` | UDP is blocked between the hub and node {node} | the UDP echo probe got no answer after 3 tries | UDP transports are skipped automatically; open UDP in the provider firewall to use them |
| `DEY-P016` | Too many port maps ({count}, max 64) | a tunnel supports at most 64 port maps | use a range such as 2000-2010, or split into two tunnels |
| `DEY-P017` | Invalid port range: {input} | a range needs start <= end and both between 1 and 65535 | write it as START-END, e.g. 2000-2010 |
| `DEY-P018` | No free port found | every candidate port is used or reserved | free a port or enter one manually |
| `DEY-P019` | Could not apply firewall rules ({firewall}) | the firewall tool returned an error | run deyroute security firewall show and see the log; check that nftables is installed |
| `DEY-P020` | Backend control port pool is exhausted | all ports in 30000-31999 are allocated | delete unused tunnels or transports |

## TLS (DEY-T0xx)

| Code | Message | Why | Fix |
| --- | --- | --- | --- |
| `DEY-T001` | Certificate expired: {path} | it expired on {expiry} | renew it: deyroute security tls renew |
| `DEY-T002` | Certificate and key do not match | the private key {key} does not belong to {cert} | provide the matching key file |
| `DEY-T003` | ACME challenge failed for {domain} | Let's Encrypt could not validate the domain; tunnel TLS fell back to auto (self-signed + pin) | make sure {domain} is DNS-only (no Cloudflare proxy) and port 80 is free, then: deyroute security tls renew |
| `DEY-T004` | Domain {domain} does not resolve to the hub | its DNS A record does not point to {ip} | set an A record for {domain} to {ip} (DNS only) and wait for propagation |
| `DEY-T005` | Certificate chain is invalid: {path} | the certificate is not signed by the provided chain | include the full chain (leaf first) in the cert file |
| `DEY-T006` | Certificate expires in {days} days: {path} | tunnel certificates are renewed automatically 30 days before expiry; this one was not | run: deyroute security tls renew |
| `DEY-T007` | Internal CA is missing: {path} | setup did not complete or secrets were deleted | restore a backup (deyroute restore FILE) or run setup again |

## Backends (DEY-B0xx)

| Code | Message | Why | Fix |
| --- | --- | --- | --- |
| `DEY-B001` | Could not download {backend} {version} | the binary could not be fetched from any source (direct, mirror or via a node) | check that a node is online (downloads go through nodes), or set DEYROUTE_MIRROR |
| `DEY-B002` | Could not render config for {backend}/{transport} | the backend template rejected the tunnel settings | see details below; run deyroute config validate |
| `DEY-B003` | Unit {unit} failed to start | the backend process exited; its last log lines are shown below | fix the cause shown in the log, then: deyroute tunnel restart <tunnel> |
| `DEY-B004` | Transport {transport} did not pass the probe within {seconds}s | the backend started but traffic did not flow through the tunnel | check the node side (deyroute logs <tunnel>), or try the next rung: deyroute tunnel switch <tunnel> --transport <id> |
| `DEY-B005` | {backend} has no binary for {arch} | the pinned release does not ship this architecture | remove transports of this backend from the ladder |
| `DEY-B006` | Transport {transport} cannot be used: {reason} | the transport's static checks failed for this tunnel | it is skipped from this tunnel's ladder and retried every 30 minutes |
| `DEY-B007` | Transport {transport} needs UDP and was skipped for tunnel {tunnel} | the UDP probe between hub and node failed | open UDP between the servers; the rung is re-tested every 30 minutes |
| `DEY-B008` | Backend {backend} is not in the manifest | backends.yaml has no entry for it | run: deyroute update manifest |
| `DEY-B009` | Key generation failed for {backend} | the backend key tool returned an error | see the log; make sure the backend binary is installed (deyroute update backends) |
| `DEY-B010` | Transport {transport} does not carry {proto} | the rung cannot forward this tunnel's protocol | it is removed from this tunnel's ladder automatically |
| `DEY-B040` | Waterwall config is not valid JSON: {file} | the rendered Waterwall file failed validation before start | report this with deyroute doctor; the rung is skipped |
| `DEY-B041` | Waterwall core.json missing in {dir} | Waterwall must start with WorkingDirectory set to the core.json folder | run deyroute tunnel restart <tunnel> to re-render |
| `DEY-B042` | No decoy SNI is reachable | none of {decoys} answered a TLS 1.3 handshake from the hub | set a reachable decoy list in Settings, then retry |
| `DEY-B043` | Waterwall failed to start | Waterwall exited; its debug log is shown below | fix the cause shown in the log or remove the rung from the ladder |
| `DEY-B044` | Waterwall Reality handshake failed | the node could not complete the Reality handshake with the hub | check that the decoy SNI is reachable and the password matches on both sides (re-render) |

## Failover (DEY-F0xx)

| Code | Message | Why | Fix |
| --- | --- | --- | --- |
| `DEY-F001` | Tunnel {tunnel}: all candidates exhausted | every transport on every node failed its probe | the ladder is retried from rung 1 every 30s (backoff to 5m); check nodes and filtering |
| `DEY-F002` | Tunnel {tunnel}: flapping limit reached ({count} switches/hour) | too many automatic switches; failover holds the current state | inspect events (deyroute events --tunnel {tunnel}); manual switch is still allowed |
| `DEY-F003` | Tunnel {tunnel}: failback to {transport} failed | the primary rung did not pass the probe within 15s; returned to the previous rung | nothing to do; the failback delay was doubled and will retry automatically |
| `DEY-F004` | Tunnel {tunnel}: failover is paused | automatic switching was paused by the owner | resume it: deyroute tunnel resume {tunnel} |
| `DEY-F005` | Tunnel {tunnel}: service {target} is down on node {node} | the service behind the tunnel is not answering on the node; switching transport would not help | start the service on the node (e.g. systemctl restart xray) or add a backup node |
| `DEY-F006` | Tunnel {tunnel}: cannot switch to {target} | the target transport or node is not part of this tunnel | list options with: deyroute tunnel show {tunnel} |

## Security / update (DEY-S0xx)

| Code | Message | Why | Fix |
| --- | --- | --- | --- |
| `DEY-S001` | Signature or checksum invalid: {file} | the file does not match the signed manifest; it was rejected and nothing changed | retry later or use another mirror; never install unverified binaries |
| `DEY-S002` | Secret file has unsafe permissions: {path} ({mode}) | secrets must be 0600 and owned by root | chmod 600 {path} && chown root:root {path} |
| `DEY-S003` | Update of {component} was rolled back | {reason} | the previous version is running again; see events and the log before retrying |
| `DEY-S004` | Could not decrypt backup {file} | wrong passphrase or damaged file | re-enter the passphrase; use the original backup file |
| `DEY-S005` | Backup file is invalid: {file} | it is not a deyroute backup or its schema could not be validated | use a file produced by deyroute backup |
| `DEY-S006` | Manifest has no sha256 for {backend} ({arch}) | binaries are never installed without a pinned checksum | run deyroute update manifest, or fill sha256 in /etc/deyroute/backends.yaml |
| `DEY-S007` | No previous binary to roll back to | /var/lib/deyroute/bin/deyroute.prev does not exist | install a specific version: deyroute update --version V |

## Internal (DEY-X0xx)

| Code | Message | Why | Fix |
| --- | --- | --- | --- |
| `DEY-X000` | Unexpected error | an internal error occurred; details are in the log | run deyroute doctor and send the generated file |
| `DEY-X001` | State database is corrupt: {path} | bbolt could not open the file; it was restored from the last backup when possible | if the problem persists: systemctl stop deyroute-hub && rm {path} && systemctl start deyroute-hub (history is lost, config is kept) |
| `DEY-X002` | systemctl not found | deyroute manages services through systemd | install systemd or use a supported distribution |
| `DEY-X003` | Daemon not running | the local API socket /run/deyroute/daemon.sock is not available | systemctl start {service} |
| `DEY-X004` | Command not allowed: {command} | deyroute only executes an allow-listed set of external programs | this is a bug; please report it with deyroute doctor |
| `DEY-X005` | nft failed | the nftables command returned an error | check that nftables is installed and the kernel supports it; see the log |
| `DEY-X006` | Local API error ({status}) | the daemon returned an unexpected response | see /var/log/deyroute/hub.log; restart with systemctl restart deyroute-hub |
| `DEY-X007` | External command failed: {command} | the program returned a non-zero exit status | see the log for its output |
| `DEY-X008` | Not implemented yet: {feature} | this feature is scheduled for a later release | check CHANGELOG.md for availability |
| `DEY-X009` | This command needs a {need}, but this server is a {role} | the command only makes sense on the other role | run it on the {need} server |

