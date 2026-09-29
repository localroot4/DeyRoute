package errors

// Catalog of every DEY code. Adding a code without Message, Why and Fix
// fails TestCatalogComplete (and therefore CI). docs/ERRORS.md is generated
// from this file with `make docs` and checked by TestErrorsDocUpToDate.
//
// Placeholders used by templates are listed next to each code.

// Installer / environment (DEY-I0xx). Codes I001-I012 are also used verbatim
// by installer/install.sh.
const (
	I001 Code = "DEY-I001" // not root
	I002 Code = "DEY-I002" // no systemd
	I003 Code = "DEY-I003" // unsupported arch {arch}
	I004 Code = "DEY-I004" // download failed from all sources {file}
	I005 Code = "DEY-I005" // checksum mismatch {file}
	I006 Code = "DEY-I006" // signature invalid {file}
	I007 Code = "DEY-I007" // neither curl nor wget
	I008 Code = "DEY-I008" // systemd too old {version}
	I009 Code = "DEY-I009" // kernel too old {version}
	I010 Code = "DEY-I010" // iproute2 missing
	I011 Code = "DEY-I011" // neither nftables nor iptables
	I012 Code = "DEY-I012" // ca-certificates missing
	I013 Code = "DEY-I013" // already set up {role}
	I014 Code = "DEY-I014" // setup step failed {step}
)

// Configuration (DEY-C0xx).
const (
	C001 Code = "DEY-C001" // unknown key {key} {line}
	C002 Code = "DEY-C002" // duplicate id {kind} {id}
	C003 Code = "DEY-C003" // duplicate listen port {port} {tunnel} {other}
	C004 Code = "DEY-C004" // invalid target {target} {tunnel}
	C005 Code = "DEY-C005" // unknown transport {transport} {valid}
	C006 Code = "DEY-C006" // schema migration failed {from} {to}
	C007 Code = "DEY-C007" // invalid id {kind} {id}
	C008 Code = "DEY-C008" // tunnel without node {tunnel}
	C009 Code = "DEY-C009" // empty ladder {tunnel}
	C010 Code = "DEY-C010" // unknown node reference {node} {tunnel}
	C011 Code = "DEY-C011" // listen collides with reserved port {port} {reason}
	C012 Code = "DEY-C012" // unknown ladder {ladder} {tunnel}
	C013 Code = "DEY-C013" // invalid value {field} {value} {allowed}
	C014 Code = "DEY-C014" // cannot read/parse config {path}
	C015 Code = "DEY-C015" // too many port maps {tunnel} {count}
	C016 Code = "DEY-C016" // wrong role section {role}
	C017 Code = "DEY-C017" // config write failed {path}
	C018 Code = "DEY-C018" // id is immutable {kind} {id}
	C019 Code = "DEY-C019" // unsupported schema version {version}
	C020 Code = "DEY-C020" // invalid port input {input}
)

// Node / control channel (DEY-N0xx).
const (
	N001 Code = "DEY-N001" // join token invalid/expired
	N002 Code = "DEY-N002" // CA fingerprint mismatch {expected} {got}
	N003 Code = "DEY-N003" // node offline {node}
	N004 Code = "DEY-N004" // version incompatible {node} {node_version} {hub_version}
	N005 Code = "DEY-N005" // command timeout {node} {command}
	N006 Code = "DEY-N006" // invalid join link {link}
	N007 Code = "DEY-N007" // join rate limited {ip}
	N008 Code = "DEY-N008" // unknown node {node}
	N009 Code = "DEY-N009" // hub unreachable {addr}
	N010 Code = "DEY-N010" // node id already joined {node}
	N011 Code = "DEY-N011" // command failed on node {node} {command}
	N012 Code = "DEY-N012" // no online node for fetch proxy
)

// Ports / firewall (DEY-P0xx).
const (
	P010 Code = "DEY-P010" // port invalid {input}
	P011 Code = "DEY-P011" // port reserved {port} {reason}
	P012 Code = "DEY-P012" // port in use {port} {process} {addr}
	P013 Code = "DEY-P013" // firewall blocks port {port} {firewall} {command}
	P014 Code = "DEY-P014" // not reachable from node {port} {node}
	P015 Code = "DEY-P015" // udp blocked {node}
	P016 Code = "DEY-P016" // too many port maps {count}
	P017 Code = "DEY-P017" // invalid range {input}
	P018 Code = "DEY-P018" // no free port found
	P019 Code = "DEY-P019" // firewall apply failed {firewall}
	P020 Code = "DEY-P020" // backend control port pool exhausted
)

// TLS (DEY-T0xx).
const (
	T001 Code = "DEY-T001" // cert expired {path} {expiry}
	T002 Code = "DEY-T002" // key mismatch {cert} {key}
	T003 Code = "DEY-T003" // acme challenge failed {domain}
	T004 Code = "DEY-T004" // domain does not resolve to hub {domain} {ip}
	T005 Code = "DEY-T005" // chain invalid {path}
	T006 Code = "DEY-T006" // cert expiring soon {path} {days}
	T007 Code = "DEY-T007" // CA missing {path}
)

// Backends (DEY-B0xx). B040-B049 are Waterwall specific.
const (
	B001 Code = "DEY-B001" // binary download failed {backend} {version}
	B002 Code = "DEY-B002" // render failed {backend} {transport}
	B003 Code = "DEY-B003" // unit failed to start {unit} (Detail = last 40 log lines)
	B004 Code = "DEY-B004" // probe timeout after start {transport} {seconds}
	B005 Code = "DEY-B005" // unsupported arch for backend {backend} {arch}
	B006 Code = "DEY-B006" // transport validation failed {transport} {reason}
	B007 Code = "DEY-B007" // transport needs UDP (skipped) {transport} {tunnel}
	B008 Code = "DEY-B008" // backend not in manifest {backend}
	B009 Code = "DEY-B009" // key generation failed {backend}
	B010 Code = "DEY-B010" // transport does not support tunnel protocol {transport} {proto}
	B040 Code = "DEY-B040" // waterwall json invalid {file}
	B041 Code = "DEY-B041" // waterwall core.json missing {dir}
	B042 Code = "DEY-B042" // no reachable decoy SNI {decoys}
	B043 Code = "DEY-B043" // waterwall failed to start (Detail = log)
	B044 Code = "DEY-B044" // waterwall reality handshake failed
)

// Failover (DEY-F0xx).
const (
	F001 Code = "DEY-F001" // all candidates exhausted {tunnel}
	F002 Code = "DEY-F002" // flapping limit reached {tunnel} {count}
	F003 Code = "DEY-F003" // failback failed {tunnel} {transport}
	F004 Code = "DEY-F004" // failover paused {tunnel}
	F005 Code = "DEY-F005" // service down on node {tunnel} {node} {target}
	F006 Code = "DEY-F006" // switch target invalid {tunnel} {target}
)

// Security / update (DEY-S0xx).
const (
	S001 Code = "DEY-S001" // manifest/binary signature or checksum invalid {file}
	S002 Code = "DEY-S002" // secrets permission wrong {path} {mode}
	S003 Code = "DEY-S003" // update rollback performed {component} {reason}
	S004 Code = "DEY-S004" // backup decrypt failed {file}
	S005 Code = "DEY-S005" // backup invalid {file}
	S006 Code = "DEY-S006" // manifest entry missing sha256 {backend} {arch}
	S007 Code = "DEY-S007" // no previous binary for rollback
)

// Internal (DEY-X0xx).
const (
	X000 Code = "DEY-X000" // unexpected error
	X001 Code = "DEY-X001" // state db corrupt {path}
	X002 Code = "DEY-X002" // systemctl not found
	X003 Code = "DEY-X003" // daemon not running {service}
	X004 Code = "DEY-X004" // command not allowed {command}
	X005 Code = "DEY-X005" // nft failed
	X006 Code = "DEY-X006" // local API error {status}
	X007 Code = "DEY-X007" // command failed {command}
	X008 Code = "DEY-X008" // not implemented yet {feature}
	X009 Code = "DEY-X009" // wrong role for command {role} {need}
)

var catalog = map[Code]Info{
	// ---------------------------------------------------------------- I
	I001: {I001, "This command must run as root",
		"deyroute manages systemd units, nftables and files under /etc and /var/lib",
		"re-run with sudo, e.g.: sudo bash install.sh"},
	I002: {I002, "systemd was not found",
		"deyroute runs its hub, node and tunnel processes as systemd units",
		"use a distribution with systemd 245 or newer (Ubuntu 22.04+, Debian 12+)"},
	I003: {I003, "Unsupported CPU architecture: {arch}",
		"release binaries exist only for linux/amd64 and linux/arm64",
		"use an amd64 or arm64 server"},
	I004: {I004, "Download failed from every source: {file}",
		"the mirror, the release base and GitHub were each tried 3 times without success",
		"pass --mirror URL, set DEYROUTE_MIRROR, or download the archive elsewhere and use --local /path/file.tar.gz"},
	I005: {I005, "Checksum mismatch for {file}",
		"the downloaded file does not match SHA256SUMS; it is corrupt or was tampered with",
		"retry; if it persists use another mirror or --local with a file you verified"},
	I006: {I006, "Signature invalid for {file}",
		"SHA256SUMS.minisig was not produced by the DEYROUTE release key",
		"use the official mirror; only for testing, --skip-signature bypasses this check"},
	I007: {I007, "Neither curl nor wget is installed",
		"the installer needs one of them to download release files",
		"apt-get install -y curl   (or: dnf install -y curl)"},
	I008: {I008, "systemd is too old ({version})",
		"deyroute needs systemd 245 or newer for unit sandboxing options",
		"upgrade the distribution to a supported release"},
	I009: {I009, "Linux kernel is too old ({version})",
		"deyroute needs kernel 5.4 or newer (BBR, nftables features)",
		"upgrade the kernel or the distribution"},
	I010: {I010, "iproute2 (the ip command) is missing",
		"public IP detection and WireGuard transports use iproute2",
		"apt-get install -y iproute2   (or: dnf install -y iproute)"},
	I011: {I011, "Neither nftables nor iptables is installed",
		"deyroute manages its own firewall table and needs one of them",
		"apt-get install -y nftables   (or: dnf install -y nftables)"},
	I012: {I012, "ca-certificates is missing",
		"HTTPS downloads cannot be verified without the system CA bundle",
		"apt-get install -y ca-certificates"},
	I013: {I013, "This server is already set up as {role}",
		"/etc/deyroute/config.yaml already exists; setup never overwrites it",
		"use the menu to change settings, or run: deyroute uninstall   then set up again"},
	I014: {I014, "Setup step failed: {step}",
		"a required setup step could not complete",
		"read the log below, fix the cause and run the installer again (it repairs, never reinstalls)"},

	// ---------------------------------------------------------------- C
	C001: {C001, "Unknown key in config: {key}",
		"config.yaml has a strict schema; unknown keys are rejected (line {line})",
		"remove or rename the key; run: deyroute config validate"},
	C002: {C002, "Duplicate {kind} id: {id}",
		"ids must be unique within their list",
		"give each {kind} a different id"},
	C003: {C003, "Listen port {port} is used by two tunnels",
		"tunnels {tunnel} and {other} both listen on {port}; only one process can bind a port",
		"change the listen port of one tunnel"},
	C004: {C004, "Invalid target '{target}' in tunnel {tunnel}",
		"a target must be host:port with a port between 1 and 65535",
		"use the form 127.0.0.1:443"},
	C005: {C005, "Unknown transport: {transport}",
		"the ladder contains a transport that no backend provides",
		"use one of: {valid}"},
	C006: {C006, "Config schema migration failed ({from} -> {to})",
		"the config file could not be converted to the current schema",
		"restore a backup from /var/lib/deyroute/backups/auto/ or fix the file by hand, then run: deyroute config validate"},
	C007: {C007, "Invalid {kind} id: '{id}'",
		"ids may contain only a-z, 0-9 and '-', 2 to 32 characters",
		"choose an id like de-1 or main"},
	C008: {C008, "Tunnel {tunnel} has no node",
		"every tunnel needs at least one node",
		"add a node: deyroute tunnel edit {tunnel}   or   deyroute tunnel backup add {tunnel} --node <id>"},
	C009: {C009, "Tunnel {tunnel} has an empty ladder",
		"at least one transport is required",
		"use the default ladder: deyroute tunnel edit {tunnel} --ladder default"},
	C010: {C010, "Tunnel {tunnel} refers to unknown node {node}",
		"the node id is not in the nodes list",
		"join the node first (deyroute node join-command) or fix the id"},
	C011: {C011, "Listen port {port} is reserved",
		"{reason}",
		"pick another port; deyroute port suggest shows free ones"},
	C012: {C012, "Unknown ladder '{ladder}' in tunnel {tunnel}",
		"the ladder name is not defined under ladders:",
		"use an existing ladder (deyroute ladder list) or create it"},
	C013: {C013, "Invalid value for {field}: '{value}'",
		"allowed values: {allowed}",
		"correct the value and run: deyroute config validate"},
	C014: {C014, "Cannot read config {path}",
		"the file is missing, unreadable or not valid YAML",
		"check the file, or restore the last good copy from /var/lib/deyroute/backups/auto/"},
	C015: {C015, "Tunnel {tunnel} has {count} port maps (max 64)",
		"a tunnel supports at most 64 port maps",
		"use a port range (e.g. 2000-2010) or split into several tunnels"},
	C016: {C016, "Config role '{role}' does not match its sections",
		"a hub config needs hub:, a node config needs node:, and not both",
		"fix role: or the sections; run: deyroute config validate"},
	C017: {C017, "Could not write config {path}",
		"the atomic write (temp file + rename) failed",
		"check free disk space and permissions on /etc/deyroute"},
	C018: {C018, "The {kind} id '{id}' cannot be changed",
		"ids are immutable after creation; only the name can change",
		"change the name instead, or delete and re-create"},
	C019: {C019, "Unsupported schema_version {version}",
		"this deyroute build does not know that schema version",
		"update deyroute (deyroute update) or restore an older config"},
	C020: {C020, "Could not understand port input '{input}'",
		"accepted forms: 443, 443/udp, 443,2053, 2000-2010, 443:8443",
		"re-enter the ports using one of the accepted forms"},

	// ---------------------------------------------------------------- N
	N001: {N001, "Join token is invalid or expired",
		"tokens are single-use and expire after 15 minutes",
		"on the hub run: deyroute node join-command   and use the new line"},
	N002: {N002, "Hub CA fingerprint mismatch",
		"the hub presented CA {got}, the join link pins {expected}; this may be a wrong address or interception",
		"copy the join command again from the hub menu; do not edit it"},
	N003: {N003, "Node {node} is offline",
		"no heartbeat from the node's control connection",
		"check the node: systemctl status deyroute-node   and its network to the hub"},
	N004: {N004, "Node {node} version {node_version} is incompatible with hub {hub_version}",
		"hub and node must share the same major.minor version",
		"update the node from the hub menu: Update -> deyroute (nodes follow the hub)"},
	N005: {N005, "Command {command} timed out on node {node}",
		"the node did not answer in time",
		"check deyroute node test {node}; see the node log with deyroute logs node"},
	N006: {N006, "Invalid join link",
		"expected dey://TOKEN@HUB_IP:PORT#SHA256_FINGERPRINT",
		"copy the full line again from the hub (deyroute node join-command)"},
	N007: {N007, "Too many failed join attempts from {ip}",
		"more than 5 failures in an hour block the address for 1 hour",
		"wait one hour, then use a fresh join command"},
	N008: {N008, "Unknown node: {node}",
		"no node with that id has joined this hub",
		"list nodes with: deyroute node list"},
	N009: {N009, "Cannot reach the hub at {addr}",
		"the control connection could not be opened",
		"check that the hub is up and that {addr} is reachable from this server (firewall, IP)"},
	N010: {N010, "A node with id {node} already joined",
		"node ids are unique per hub",
		"use --name to pick another id, or remove the old node on the hub first"},
	N011: {N011, "Command {command} failed on node {node}",
		"the node reported an error while running the command",
		"see details below and the node log: deyroute logs node"},
	N012: {N012, "No online node available to download through",
		"the hub fetches files via a node because GitHub is often unreachable from Iran",
		"bring a node online, or set DEYROUTE_MIRROR to a reachable mirror"},

	// ---------------------------------------------------------------- P
	P010: {P010, "Invalid port: {input}",
		"ports must be numbers between 1 and 65535",
		"enter a port like 443 or 443/udp"},
	P011: {P011, "Port {port} is reserved",
		"{reason}",
		"pick another port; deyroute port suggest shows free ones"},
	P012: {P012, "Port {port} is already in use",
		"{process} is listening on {addr}",
		"choose another port, or stop that service first"},
	P013: {P013, "Firewall {firewall} blocks port {port}",
		"an external firewall rule drops traffic to this port",
		"allow it: {command}"},
	P014: {P014, "Port {port} is not reachable from node {node}",
		"the node could not connect to the hub's public IP on this port (datacenter firewall or routing)",
		"open the port in your provider's firewall panel; run deyroute port check {port} again"},
	P015: {P015, "UDP is blocked between the hub and node {node}",
		"the UDP echo probe got no answer after 3 tries",
		"UDP transports are skipped automatically; open UDP in the provider firewall to use them"},
	P016: {P016, "Too many port maps ({count}, max 64)",
		"a tunnel supports at most 64 port maps",
		"use a range such as 2000-2010, or split into two tunnels"},
	P017: {P017, "Invalid port range: {input}",
		"a range needs start <= end and both between 1 and 65535",
		"write it as START-END, e.g. 2000-2010"},
	P018: {P018, "No free port found",
		"every candidate port is used or reserved",
		"free a port or enter one manually"},
	P019: {P019, "Could not apply firewall rules ({firewall})",
		"the firewall tool returned an error",
		"run deyroute security firewall show and see the log; check that nftables is installed"},
	P020: {P020, "Backend control port pool is exhausted",
		"all ports in 30000-31999 are allocated",
		"delete unused tunnels or transports"},

	// ---------------------------------------------------------------- T
	T001: {T001, "Certificate expired: {path}",
		"it expired on {expiry}",
		"renew it: deyroute security tls renew"},
	T002: {T002, "Certificate and key do not match",
		"the private key {key} does not belong to {cert}",
		"provide the matching key file"},
	T003: {T003, "ACME challenge failed for {domain}",
		"Let's Encrypt could not validate the domain; tunnel TLS fell back to auto (self-signed + pin)",
		"make sure {domain} is DNS-only (no Cloudflare proxy) and port 80 is free, then: deyroute security tls renew"},
	T004: {T004, "Domain {domain} does not resolve to the hub",
		"its DNS A record does not point to {ip}",
		"set an A record for {domain} to {ip} (DNS only) and wait for propagation"},
	T005: {T005, "Certificate chain is invalid: {path}",
		"the certificate is not signed by the provided chain",
		"include the full chain (leaf first) in the cert file"},
	T006: {T006, "Certificate expires in {days} days: {path}",
		"tunnel certificates are renewed automatically 30 days before expiry; this one was not",
		"run: deyroute security tls renew"},
	T007: {T007, "Internal CA is missing: {path}",
		"setup did not complete or secrets were deleted",
		"restore a backup (deyroute restore FILE) or run setup again"},

	// ---------------------------------------------------------------- B
	B001: {B001, "Could not download {backend} {version}",
		"the binary could not be fetched from any source (direct, mirror or via a node)",
		"check that a node is online (downloads go through nodes), or set DEYROUTE_MIRROR"},
	B002: {B002, "Could not render config for {backend}/{transport}",
		"the backend template rejected the tunnel settings",
		"see details below; run deyroute config validate"},
	B003: {B003, "Unit {unit} failed to start",
		"the backend process exited; its last log lines are shown below",
		"fix the cause shown in the log, then: deyroute tunnel restart <tunnel>"},
	B004: {B004, "Transport {transport} did not pass the probe within {seconds}s",
		"the backend started but traffic did not flow through the tunnel",
		"check the node side (deyroute logs <tunnel>), or try the next rung: deyroute tunnel switch <tunnel> --transport <id>"},
	B005: {B005, "{backend} has no binary for {arch}",
		"the pinned release does not ship this architecture",
		"remove transports of this backend from the ladder"},
	B006: {B006, "Transport {transport} cannot be used: {reason}",
		"the transport's static checks failed for this tunnel",
		"it is skipped from this tunnel's ladder and retried every 30 minutes"},
	B007: {B007, "Transport {transport} needs UDP and was skipped for tunnel {tunnel}",
		"the UDP probe between hub and node failed",
		"open UDP between the servers; the rung is re-tested every 30 minutes"},
	B008: {B008, "Backend {backend} is not in the manifest",
		"backends.yaml has no entry for it",
		"run: deyroute update manifest"},
	B009: {B009, "Key generation failed for {backend}",
		"the backend key tool returned an error",
		"see the log; make sure the backend binary is installed (deyroute update backends)"},
	B010: {B010, "Transport {transport} does not carry {proto}",
		"the rung cannot forward this tunnel's protocol",
		"it is removed from this tunnel's ladder automatically"},
	B040: {B040, "Waterwall config is not valid JSON: {file}",
		"the rendered Waterwall file failed validation before start",
		"report this with deyroute doctor; the rung is skipped"},
	B041: {B041, "Waterwall core.json missing in {dir}",
		"Waterwall must start with WorkingDirectory set to the core.json folder",
		"run deyroute tunnel restart <tunnel> to re-render"},
	B042: {B042, "No decoy SNI is reachable",
		"none of {decoys} answered a TLS 1.3 handshake from the hub",
		"set a reachable decoy list in Settings, then retry"},
	B043: {B043, "Waterwall failed to start",
		"Waterwall exited; its debug log is shown below",
		"fix the cause shown in the log or remove the rung from the ladder"},
	B044: {B044, "Waterwall Reality handshake failed",
		"the node could not complete the Reality handshake with the hub",
		"check that the decoy SNI is reachable and the password matches on both sides (re-render)"},

	// ---------------------------------------------------------------- F
	F001: {F001, "Tunnel {tunnel}: all candidates exhausted",
		"every transport on every node failed its probe",
		"the ladder is retried from rung 1 every 30s (backoff to 5m); check nodes and filtering"},
	F002: {F002, "Tunnel {tunnel}: flapping limit reached ({count} switches/hour)",
		"too many automatic switches; failover holds the current state",
		"inspect events (deyroute events --tunnel {tunnel}); manual switch is still allowed"},
	F003: {F003, "Tunnel {tunnel}: failback to {transport} failed",
		"the primary rung did not pass the probe within 15s; returned to the previous rung",
		"nothing to do; the failback delay was doubled and will retry automatically"},
	F004: {F004, "Tunnel {tunnel}: failover is paused",
		"automatic switching was paused by the owner",
		"resume it: deyroute tunnel resume {tunnel}"},
	F005: {F005, "Tunnel {tunnel}: service {target} is down on node {node}",
		"the service behind the tunnel is not answering on the node; switching transport would not help",
		"start the service on the node (e.g. systemctl restart xray) or add a backup node"},
	F006: {F006, "Tunnel {tunnel}: cannot switch to {target}",
		"the target transport or node is not part of this tunnel",
		"list options with: deyroute tunnel show {tunnel}"},

	// ---------------------------------------------------------------- S
	S001: {S001, "Signature or checksum invalid: {file}",
		"the file does not match the signed manifest; it was rejected and nothing changed",
		"retry later or use another mirror; never install unverified binaries"},
	S002: {S002, "Secret file has unsafe permissions: {path} ({mode})",
		"secrets must be 0600 and owned by root",
		"chmod 600 {path} && chown root:root {path}"},
	S003: {S003, "Update of {component} was rolled back",
		"{reason}",
		"the previous version is running again; see events and the log before retrying"},
	S004: {S004, "Could not decrypt backup {file}",
		"wrong passphrase or damaged file",
		"re-enter the passphrase; use the original backup file"},
	S005: {S005, "Backup file is invalid: {file}",
		"it is not a deyroute backup or its schema could not be validated",
		"use a file produced by deyroute backup"},
	S006: {S006, "Manifest has no sha256 for {backend} ({arch})",
		"binaries are never installed without a pinned checksum",
		"run deyroute update manifest, or fill sha256 in /etc/deyroute/backends.yaml"},
	S007: {S007, "No previous binary to roll back to",
		"/var/lib/deyroute/bin/deyroute.prev does not exist",
		"install a specific version: deyroute update --version V"},

	// ---------------------------------------------------------------- X
	X000: {X000, "Unexpected error",
		"an internal error occurred; details are in the log",
		"run deyroute doctor and send the generated file"},
	X001: {X001, "State database is corrupt: {path}",
		"bbolt could not open the file; it was restored from the last backup when possible",
		"if the problem persists: systemctl stop deyroute-hub && rm {path} && systemctl start deyroute-hub (history is lost, config is kept)"},
	X002: {X002, "systemctl not found",
		"deyroute manages services through systemd",
		"install systemd or use a supported distribution"},
	X003: {X003, "Daemon not running",
		"the local API socket /run/deyroute/daemon.sock is not available",
		"systemctl start {service}"},
	X004: {X004, "Command not allowed: {command}",
		"deyroute only executes an allow-listed set of external programs",
		"this is a bug; please report it with deyroute doctor"},
	X005: {X005, "nft failed",
		"the nftables command returned an error",
		"check that nftables is installed and the kernel supports it; see the log"},
	X006: {X006, "Local API error ({status})",
		"the daemon returned an unexpected response",
		"see /var/log/deyroute/hub.log; restart with systemctl restart deyroute-hub"},
	X007: {X007, "External command failed: {command}",
		"the program returned a non-zero exit status",
		"see the log for its output"},
	X008: {X008, "Not implemented yet: {feature}",
		"this feature is scheduled for a later release",
		"check CHANGELOG.md for availability"},
	X009: {X009, "This command needs a {need}, but this server is a {role}",
		"the command only makes sense on the other role",
		"run it on the {need} server"},
}
