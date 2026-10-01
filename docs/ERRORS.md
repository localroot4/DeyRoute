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
| `DEY-I020` | Could not detect the public IP address of this server | {reason} | check the network with: ip route   then enter this server's public IP when setup asks for it |
| `DEY-I021` | Detected address {ip} is not a public IP | the route to the internet uses a private, CGNAT or loopback source address; nodes abroad cannot connect to it unless the provider forwards it to this server | enter the server's real public IP when setup asks (see the provider panel), or later change hub.public_ip in /etc/deyroute/config.yaml and run: deyroute config apply |
| `DEY-I022` | Uninstall step failed: {step} | part of the removal could not complete; every other step still ran and the deyroute binary was kept | fix the cause shown below, then run deyroute uninstall again (it is safe to repeat) |
| `DEY-I023` | This server is not set up yet | /etc/deyroute/config.yaml does not exist, so no deyroute service runs here | on the Iran server run: deyroute setup   on a foreign server run the join command your hub shows |

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
| `DEY-C008` | Tunnel {tunnel} has no node | every tunnel needs at least one node, and this change would leave it without one | give it another node first: deyroute tunnel backup add {tunnel} --node <id>   or delete it: deyroute tunnel delete {tunnel} |
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
| `DEY-C021` | Unknown tunnel: {tunnel} | no tunnel with that id exists in config.yaml | list the tunnels with: deyroute tunnel list |
| `DEY-C022` | Ladder '{ladder}' is built in and cannot be changed | the builtin ladders (default, udp-default) follow section 8 of the specification and are read-only here | create your own ladder: deyroute ladder create <name> --rungs a,b,c   then: deyroute tunnel edit <id> --ladder <name> |
| `DEY-C023` | Ladder '{ladder}' is in use | tunnels {tunnels} use this ladder | move those tunnels to another ladder first (deyroute tunnel edit <id> --ladder default), then delete it |
| `DEY-C024` | {path} changed while it was being edited | another deyroute command or the menu saved {path} after the editor was opened; saving the edited copy would undo that change | your edited copy is kept in {copy}: run deyroute config edit again and make your changes on the current file |
| `DEY-C050` | Telegram rejected the notification settings (HTTP {status}) | Telegram answered '{reason}': the bot token is wrong, the chat id is unknown, or the bot is not a member of that chat | send /start to the bot (or add it to the group), then: deyroute notify telegram set --token-file F --chat-id C   and   deyroute notify telegram test |

## Node / control channel (DEY-N0xx)

| Code | Message | Why | Fix |
| --- | --- | --- | --- |
| `DEY-N001` | Join token is invalid or expired | tokens are single-use and expire after 15 minutes | on the hub run: deyroute node join-command   and use the new line |
| `DEY-N002` | Hub CA fingerprint mismatch | the hub presented CA {got}, the join link pins {expected}; this may be a wrong address or interception | copy the join command again from the hub menu; do not edit it |
| `DEY-N003` | Node {node} is offline | no heartbeat from the node's control connection | check the node: systemctl status deyroute-node   and its network to the hub |
| `DEY-N004` | Node {node} version {node_version} is incompatible with hub {hub_version} | hub and node must share the same major.minor version | run deyroute update on the hub (its nodes then update from it), or re-run the installer on this node with --version {hub_version} |
| `DEY-N005` | Command {command} timed out on node {node} | the node did not answer in time | check deyroute node test {node}; see the node log with deyroute logs node |
| `DEY-N006` | Invalid join link | expected dey://TOKEN@HUB_IP:PORT#SHA256_FINGERPRINT | copy the full line again from the hub (deyroute node join-command) |
| `DEY-N007` | Too many failed join attempts from {ip} | more than 5 failures in an hour block the address for 1 hour | wait one hour, then use a fresh join command |
| `DEY-N008` | Unknown node: {node} | no node with that id has joined this hub | list nodes with: deyroute node list |
| `DEY-N009` | Cannot reach the hub at {addr} | the control connection could not be opened | check that the hub is up and that {addr} is reachable from this server (firewall, IP) |
| `DEY-N010` | A node with id {node} already joined | node ids are unique per hub | use --name to pick another id, or remove the old node on the hub first |
| `DEY-N011` | Command {command} failed on node {node} | the node reported an error while running the command | see details below and the node log: deyroute logs node |
| `DEY-N012` | No online node available to download through | the hub fetches files via a node because GitHub is often unreachable from Iran | bring a node online, or set DEYROUTE_MIRROR to a reachable mirror |
| `DEY-N013` | Control API request refused: no valid node certificate | {path} is only served to nodes that joined this hub (mTLS client certificate of the hub CA, known node id) | join this server again: on the hub run deyroute node join-command and run the printed line here |
| `DEY-N014` | Command {command} on node {node} was cancelled | the hub stopped waiting for the command (the operation was aborted or the control connection ended) | run the operation again; if it keeps happening check deyroute node test {node} |
| `DEY-N015` | Control channel protocol error with node {node} | hub and node could not understand each other ({reason}); usually their versions differ | update the node from the hub (Update -> deyroute), then check: deyroute logs node |
| `DEY-N020` | The hub's join answer cannot be used | {reason} | make sure the hub and this server run the same deyroute version, then create a new join command on the hub (deyroute node join-command) and run it here |
| `DEY-N050` | Node {node} refused command {command} | {reason}; the node only writes below /etc/deyroute/backends, runs deyroute's own binaries and reaches only allowed addresses | update the hub and the node to the same version (Update -> deyroute); if it repeats run deyroute doctor --node {node} and report it |
| `DEY-N051` | Node {node} could not download {file} | the node tried the source 3 times without success (no outbound HTTPS, DNS failure, or the server refused); the cause is shown below | check outbound HTTPS on the node (curl -I https://github.com), or set DEYROUTE_MIRROR on the hub to a reachable mirror |

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
| `DEY-P021` | Port {port} is listed twice with different targets | one listen port can forward to only one target, but {target} and {other} were both given | keep a single entry for {port}, e.g. 443:8443 or just 443 |
| `DEY-P030` | No free network index for tunnel {tunnel} | every per-tunnel subnet index 1-{max} (WireGuard addressing) is already assigned | delete unused tunnels that use wireguard transports, then retry |
| `DEY-P031` | deyroute does not manage the firewall | security.firewall_managed is false: table inet deyroute is not applied, so the control port, the backend control ports and the tunnel ports must be opened by hand | run deyroute security firewall show for the suggested commands, or set security.firewall_managed: true and run deyroute config apply |

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
| `DEY-T008` | Cannot use certificate or key: {path} | {reason} | provide an unencrypted PEM file (BEGIN CERTIFICATE / BEGIN PRIVATE KEY); for files under /etc/deyroute/secrets restore a backup (deyroute restore FILE) |
| `DEY-T009` | Invalid certificate signing request | {reason} | run the join command again on the node; if it keeps failing run deyroute doctor on both servers |

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
| `DEY-B011` | Backend {unit} crashed on {where} | the backend process exited and systemd restarted it (restarted {restarts} time(s) since it was started); connections through the tunnel were dropped | see the backend log: deyroute logs <tunnel>; if it keeps crashing the failover engine moves to the next rung, or run: deyroute tunnel restart <tunnel> |
| `DEY-B040` | Waterwall config is not valid JSON: {file} | the rendered Waterwall file failed validation before start | report this with deyroute doctor; the rung is skipped |
| `DEY-B041` | Waterwall core.json missing in {dir} | Waterwall must start with WorkingDirectory set to the core.json folder | run deyroute tunnel restart <tunnel> to re-render |
| `DEY-B042` | No decoy SNI is reachable | none of {decoys} answered a TLS 1.3 handshake from the hub | list reachable TLS 1.3 sites in hub.decoy_snis with: deyroute config edit |
| `DEY-B043` | Waterwall failed to start | Waterwall exited; its debug log is shown below | fix the cause shown in the log or remove the rung from the ladder |
| `DEY-B044` | Waterwall Reality handshake failed | the node could not complete the Reality handshake with the hub | check that the decoy SNI is reachable and the password matches on both sides (re-render) |
| `DEY-B060` | Relay config is invalid: {path} | the direct/native relay could not use its rendered file ({reason}) | re-render the tunnel: deyroute tunnel restart <tunnel>; if it repeats run deyroute doctor |
| `DEY-B061` | Relay cannot listen on {addr} ({proto}) | {reason} | free the port (ss -lntup shows the owner) or pick another listen port; deyroute port check <port> explains conflicts |
| `DEY-B062` | Service {target} is not reachable on the node's public address | direct/haproxy connects from the hub straight to the node service, but nothing answered on {tried}; the service listens only on 127.0.0.1 or a firewall blocks it | make the service listen on 0.0.0.0 and allow the hub IP in the node firewall, or use direct/native instead |
| `DEY-B070` | WireGuard interface {iface} could not be set up | {reason} | check that the wireguard kernel module loads (modprobe wireguard) and see the tunnel log (deyroute logs <tunnel>); or use awg/userspace, which needs no kernel module |
| `DEY-B071` | AmneziaWG interface {iface} could not be configured | {reason} | check that the awg unit runs (systemctl status 'deyroute-tun@*awg-userspace*') and read the tunnel log (deyroute logs <tunnel>) |
| `DEY-B072` | WireGuard config {path} is invalid | {reason} | re-render the tunnel (deyroute tunnel restart <tunnel>); if it repeats run deyroute doctor |

## Failover (DEY-F0xx)

| Code | Message | Why | Fix |
| --- | --- | --- | --- |
| `DEY-F001` | Tunnel {tunnel}: all candidates exhausted | every transport on every node failed its probe | the ladder is retried from rung 1 every 30s (backoff to 5m); check nodes and filtering |
| `DEY-F002` | Tunnel {tunnel}: flapping limit reached ({count} switches/hour) | too many automatic switches; failover holds the current state | inspect events (deyroute events --tunnel {tunnel}); manual switch is still allowed |
| `DEY-F003` | Tunnel {tunnel}: failback to {transport} failed | the primary rung did not pass the probe within 15s; returned to the previous rung | nothing to do; the failback delay was doubled and will retry automatically |
| `DEY-F004` | Tunnel {tunnel}: failover is paused | automatic switching was paused by the owner | resume it: deyroute tunnel resume {tunnel} |
| `DEY-F005` | Tunnel {tunnel}: service {target} is down on node {node} | the service behind the tunnel is not answering on the node; switching transport would not help | start the service on the node (e.g. systemctl restart xray) or add a backup node |
| `DEY-F006` | Tunnel {tunnel}: cannot switch to {target} | the target transport or node is not part of this tunnel | list options with: deyroute tunnel show {tunnel} |
| `DEY-F007` | Tunnel {tunnel}: failover engine is not running | the tunnel is disabled, or the hub service is starting or stopping | check deyroute status; enable the tunnel (deyroute tunnel enable {tunnel}) or restart the hub: systemctl restart deyroute-hub |
| `DEY-F008` | Tunnel {tunnel}: {command} did not finish in time | the request timed out or was cancelled while the failover engine was busy (switching or testing the ladder) | check the current state with: deyroute tunnel show {tunnel}; then retry the command if needed |

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
| `DEY-S008` | A backup passphrase is required | backups contain the CA key and tunnel secrets and are encrypted with age by default | enter a passphrase, or run deyroute backup --no-encrypt and keep the file private |
| `DEY-S009` | Cannot use secret file {path} | the file is missing, empty, unreadable or damaged: {reason} | check the file (ls -l {path}); restore it from a backup (deyroute restore FILE) or recreate it through the menu |

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
| `DEY-X020` | State database is in use: {path} | another deyroute process holds the database lock (only one hub or node daemon may run) | stop the other process (systemctl stop deyroute-hub deyroute-node) or wait for it to exit, then retry |
| `DEY-X021` | State database operation failed: {op} | bbolt returned an error for {path} (disk full, I/O error or a damaged record) | check free space and disk health (df -h /var/lib/deyroute); if it persists run deyroute doctor |
| `DEY-X022` | Cannot write log file {path} | the log directory is missing, not writable or the disk is full | check permissions and free space: ls -ld /var/log/deyroute; df -h /var/log |
| `DEY-X030` | Program not installed: {command} | deyroute needs {command} but it is not in PATH or the standard system directories | install the distribution package that provides {command} (e.g. apt install iproute2 nftables), then retry |
| `DEY-X031` | External command did not finish: {command} | it was stopped because its time limit was reached or deyroute was shutting down | check system load (uptime) and the service log; retry the action |
| `DEY-X032` | Cannot write system file {path} | the directory is missing or read-only, the disk is full, or deyroute is not running as root | run deyroute as root and check free space and mounts: df -h; mount \| grep ' / ' |
| `DEY-X033` | Cannot change kernel setting {key} | writing {value} to /proc/sys failed (read-only /proc/sys in a container, or a value this kernel rejects) | run on the host as root (not in an unprivileged container); undo all tuning with: deyroute optimize revert |
| `DEY-X034` | Invalid systemd unit data: {field}='{value}' | deyroute produced a unit name or setting that systemd would reject or misread | this is a bug; run deyroute doctor and report the generated file |
| `DEY-X040` | Another deyroute daemon is already running | the local API socket {path} is answered by another process (only one hub or node daemon may run) | stop the other instance first: systemctl stop deyroute-hub deyroute-node; then start the service again |
| `DEY-X041` | Cannot open the local API socket {path} | {reason} | check that /run/deyroute is writable by root and the path is not used by another file, then: systemctl restart deyroute-hub |
| `DEY-X042` | The daemon did not finish {method} in time | the call was cancelled or exceeded its time limit before the daemon answered | check the daemon: systemctl status {service}; see deyroute logs hub; then try again |
| `DEY-X050` | Telegram message could not be delivered | api.telegram.org was not reachable directly or through any online node ({reason}) | check outbound HTTPS (or https_proxy) on the hub and nodes, then: deyroute notify telegram test; events are still in: deyroute events |
| `DEY-X051` | Probe helper {service} on {addr} stopped | accepting or reading on its socket failed unexpectedly | restart the service (systemctl restart deyroute-node, or deyroute-hub on the hub); if it repeats run: deyroute doctor |
| `DEY-X052` | Speed test to {addr} failed during {phase} | {reason} | check the tunnel first: deyroute diag probe <tunnel>; then retry with a shorter test: deyroute diag speed <tunnel> --seconds 5 |
| `DEY-X060` | Doctor file not written: {file} still contains {what} | the final check found secret material that the central filter did not remove, so no file was created (nothing leaked) | send the summary printed on the screen instead of the file and report this bug with the DEY code; logs are in /var/log/deyroute |

