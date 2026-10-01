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
	I020 Code = "DEY-I020" // public IP not detected {reason}
	I021 Code = "DEY-I021" // detected IP is private/CGNAT/loopback {ip}
	I022 Code = "DEY-I022" // uninstall step failed {step}
	I023 Code = "DEY-I023" // this server is not set up
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
	C021 Code = "DEY-C021" // unknown tunnel {tunnel}
	C022 Code = "DEY-C022" // builtin ladder is read-only {ladder}
	C023 Code = "DEY-C023" // ladder in use {ladder} {tunnels}
	C024 Code = "DEY-C024" // config.yaml changed during config edit {path} {copy}
	C025 Code = "DEY-C025" // invalid command line {reason} {command}
	C026 Code = "DEY-C026" // config.yaml holds an edit that is not applied {path}
	C050 Code = "DEY-C050" // telegram rejected token/chat id {status} {reason}
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
	N013 Code = "DEY-N013" // control API request without a valid node certificate {path}
	N014 Code = "DEY-N014" // command cancelled {node} {command}
	N015 Code = "DEY-N015" // control channel protocol error {node} {reason}
	N020 Code = "DEY-N020" // hub join answer unusable {reason}
	N050 Code = "DEY-N050" // node refused a hub command {node} {command} {reason}
	N051 Code = "DEY-N051" // node could not download a file {node} {file}
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
	P021 Code = "DEY-P021" // one listen port given two targets {port} {target} {other}
	P030 Code = "DEY-P030" // per-tunnel network index pool exhausted {tunnel} {max}
	P031 Code = "DEY-P031" // firewall not managed by deyroute (security.firewall_managed: false)
	P032 Code = "DEY-P032" // firewall command is not the confirmed one {port} {firewall} {command}
	P033 Code = "DEY-P033" // could not open a port in the external firewall {port} {firewall} {reason}
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
	T008 Code = "DEY-T008" // certificate/key file unreadable or not PEM {path} {reason}
	T009 Code = "DEY-T009" // certificate signing request invalid {reason}
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
	B011 Code = "DEY-B011" // backend crashed, systemd restarted it {unit} {where} {restarts}
	B040 Code = "DEY-B040" // waterwall json invalid {file}
	B041 Code = "DEY-B041" // waterwall core.json missing {dir}
	B042 Code = "DEY-B042" // no reachable decoy SNI {decoys}
	B043 Code = "DEY-B043" // waterwall failed to start (Detail = log)
	B044 Code = "DEY-B044" // waterwall reality handshake failed
	B060 Code = "DEY-B060" // direct relay config invalid {path} {reason}
	B061 Code = "DEY-B061" // direct relay cannot listen {addr} {proto} {reason}
	B062 Code = "DEY-B062" // direct/haproxy target not reachable on a public address {target} {tried}
	B070 Code = "DEY-B070" // wireguard kernel interface setup failed {iface} {reason}
	B071 Code = "DEY-B071" // amneziawg-go device not configurable {iface} {reason}
	B072 Code = "DEY-B072" // wireguard wg.json invalid {path} {reason}
)

// Failover (DEY-F0xx).
const (
	F001 Code = "DEY-F001" // all candidates exhausted {tunnel}
	F002 Code = "DEY-F002" // flapping limit reached {tunnel} {count}
	F003 Code = "DEY-F003" // failback failed {tunnel} {transport}
	F004 Code = "DEY-F004" // failover paused {tunnel}
	F005 Code = "DEY-F005" // service down on node {tunnel} {node} {target}
	F006 Code = "DEY-F006" // switch target invalid {tunnel} {target}
	F007 Code = "DEY-F007" // failover engine not running {tunnel}
	F008 Code = "DEY-F008" // failover command did not finish in time {tunnel} {command}
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
	S008 Code = "DEY-S008" // backup passphrase required
	S009 Code = "DEY-S009" // secret file unreadable or damaged {path} {reason}
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
	X020 Code = "DEY-X020" // state db locked by another process {path}
	X021 Code = "DEY-X021" // state db operation failed {op} {path}
	X022 Code = "DEY-X022" // log file cannot be opened/written {path}
	X030 Code = "DEY-X030" // program not installed {command}
	X031 Code = "DEY-X031" // command timed out / cancelled {command}
	X032 Code = "DEY-X032" // system file write failed {path}
	X033 Code = "DEY-X033" // kernel setting (sysctl) write failed {key} {value}
	X034 Code = "DEY-X034" // invalid systemd unit data {field} {value}
	X040 Code = "DEY-X040" // local API socket already served by another daemon {path}
	X041 Code = "DEY-X041" // local API socket cannot be opened {path} {reason}
	X042 Code = "DEY-X042" // local API call did not finish {method} {service}
	X050 Code = "DEY-X050" // telegram message not delivered {reason}
	X051 Code = "DEY-X051" // probe helper server stopped {service} {addr}
	X052 Code = "DEY-X052" // speed test failed {addr} {phase} {reason}
	X060 Code = "DEY-X060" // doctor file refused: a secret survived redaction {file} {what}
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
	I020: {I020, "Could not detect the public IP address of this server",
		"{reason}",
		"check the network with: ip route   then enter this server's public IP when setup asks for it"},
	I021: {I021, "Detected address {ip} is not a public IP",
		"the route to the internet uses a private, CGNAT or loopback source address; nodes abroad cannot connect to it unless the provider forwards it to this server",
		"enter the server's real public IP when setup asks (see the provider panel), or later change hub.public_ip in /etc/deyroute/config.yaml and run: deyroute config apply"},
	I022: {I022, "Uninstall step failed: {step}",
		"part of the removal could not complete; every other step still ran and the deyroute binary was kept",
		"fix the cause shown below, then run deyroute uninstall again (it is safe to repeat)"},
	I023: {I023, "This server is not set up yet",
		"/etc/deyroute/config.yaml does not exist, so no deyroute service runs here",
		"on the Iran server run: deyroute setup   on a foreign server run the join command your hub shows"},

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
		"every tunnel needs at least one node, and this change would leave it without one",
		"give it another node first: deyroute tunnel backup add {tunnel} --node <id>   or delete it: deyroute tunnel delete {tunnel}"},
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
		"a tunnel supports at most 64 port maps, and a range counts one map per port",
		"put the other ports in another tunnel (deyroute tunnel add), or remove ports the tunnel no longer needs (deyroute port remove)"},
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
	C021: {C021, "Unknown tunnel: {tunnel}",
		"no tunnel with that id exists in config.yaml",
		"list the tunnels with: deyroute tunnel list"},
	C022: {C022, "Ladder '{ladder}' is built in and cannot be changed",
		"the builtin ladders (default, udp-default) follow section 8 of the specification and are read-only here",
		"create your own ladder: deyroute ladder create <name> --rungs a,b,c   then: deyroute tunnel edit <id> --ladder <name>"},
	C023: {C023, "Ladder '{ladder}' is in use",
		"tunnels {tunnels} use this ladder",
		"move those tunnels to another ladder first (deyroute tunnel edit <id> --ladder default), then delete it"},
	C024: {C024, "{path} changed while it was being edited",
		"another deyroute command or the menu saved {path} after the editor was opened; saving the edited copy would undo that change",
		"your edited copy is kept in {copy}: run deyroute config edit again and make your changes on the current file"},
	C025: {C025, "Invalid command line: {reason}",
		"{command} does not accept it: an unknown command or flag, a flag without its value, or the wrong number of arguments",
		"see the usage and the examples: {command} --help"},
	C026: {C026, "{path} has changes that are not applied",
		"the file was edited after the running configuration was loaded and deyroute config apply was not run (or it refused the edit); deyroute changes nothing now, so that edit is neither overwritten nor taken over without the checks of config apply",
		"apply the edit: deyroute config apply (it names any problem; correct it with deyroute config edit), or undo it; then try again"},
	C050: {C050, "Telegram rejected the notification settings (HTTP {status})",
		"Telegram answered '{reason}': the bot token is wrong, the chat id is unknown, or the bot is not a member of that chat",
		"send /start to the bot (or add it to the group), then: deyroute notify telegram set --token-file F --chat-id C   and   deyroute notify telegram test"},

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
		"run deyroute update on the hub (its nodes then update from it), or re-run the installer on this node with --version {hub_version}"},
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
	N013: {N013, "Control API request refused: no valid node certificate",
		"{path} is only served to nodes that joined this hub (mTLS client certificate of the hub CA, known node id)",
		"join this server again: on the hub run deyroute node join-command and run the printed line here"},
	N014: {N014, "Command {command} on node {node} was cancelled",
		"the hub stopped waiting for the command (the operation was aborted or the control connection ended)",
		"run the operation again; if it keeps happening check deyroute node test {node}"},
	N015: {N015, "Control channel protocol error with node {node}",
		"hub and node could not understand each other ({reason}); usually their versions differ",
		"update the node from the hub (Update -> deyroute), then check: deyroute logs node"},
	N020: {N020, "The hub's join answer cannot be used",
		"{reason}",
		"make sure the hub and this server run the same deyroute version, then create a new join command on the hub (deyroute node join-command) and run it here"},
	N050: {N050, "Node {node} refused command {command}",
		"{reason}; the node only writes below /etc/deyroute/backends, runs deyroute's own binaries and reaches only allowed addresses",
		"update the hub and the node to the same version (Update -> deyroute); if it repeats run deyroute doctor --node {node} and report it"},
	N051: {N051, "Node {node} could not download {file}",
		"the node tried the source 3 times without success (no outbound HTTPS, DNS failure, or the server refused); the cause is shown below",
		"check outbound HTTPS on the node (curl -I https://github.com), or set DEYROUTE_MIRROR on the hub to a reachable mirror"},

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
		"a tunnel supports at most 64 port maps, and a range such as 2000-2100 counts one map per port",
		"split the ports over several tunnels of up to 64 ports each, e.g. 2000-2063 and 2064-2100"},
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
	P021: {P021, "Port {port} is listed twice with different targets",
		"one listen port can forward to only one target, but {target} and {other} were both given",
		"keep a single entry for {port}, e.g. 443:8443 or just 443"},
	P030: {P030, "No free network index for tunnel {tunnel}",
		"every per-tunnel subnet index 1-{max} (WireGuard addressing) is already assigned",
		"delete unused tunnels that use wireguard transports, then retry"},
	P031: {P031, "deyroute does not manage the firewall",
		"security.firewall_managed is false: table inet deyroute is not applied, so the control port, the backend control ports and the tunnel ports must be opened by hand",
		"run deyroute security firewall show for the suggested commands, or set security.firewall_managed: true and run deyroute config apply"},
	P032: {P032, "Port {port}: the firewall command is not the one you confirmed",
		"the firewall was checked again and opening the port in {firewall} now needs: {command}; deyroute runs only the exact command the owner confirmed",
		"check the port again (deyroute port check {port} --open) and confirm the command it shows"},
	P033: {P033, "Could not open port {port} in {firewall}",
		"{reason}",
		"open the port by hand with the firewall's own tool, then run deyroute port check {port} again"},

	// ---------------------------------------------------------------- T
	T001: {T001, "Certificate expired: {path}",
		"it expired on {expiry}",
		"renew it: deyroute security tls renew"},
	T002: {T002, "Certificate and key do not match",
		"the private key {key} does not belong to {cert}",
		"provide the matching key file"},
	T003: {T003, "ACME challenge failed for {domain}",
		"Let's Encrypt could not validate the domain; tunnel TLS fell back to auto (self-signed + pin)",
		"make sure {domain} is DNS-only (no Cloudflare proxy) and port 80 is free (or use DNS-01: deyroute security tls acme --cloudflare-token-file F), then: deyroute security tls renew"},
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
	T008: {T008, "Cannot use certificate or key: {path}",
		"{reason}",
		"provide an unencrypted PEM file (BEGIN CERTIFICATE / BEGIN PRIVATE KEY); for files under /etc/deyroute/secrets restore a backup (deyroute restore FILE)"},
	T009: {T009, "Invalid certificate signing request",
		"{reason}",
		"run the join command again on the node; if it keeps failing run deyroute doctor on both servers"},

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
	B011: {B011, "Backend {unit} crashed on {where}",
		"the backend process exited and systemd restarted it (restarted {restarts} time(s) since it was started); connections through the tunnel were dropped",
		"see the backend log: deyroute logs <tunnel>; if it keeps crashing the failover engine moves to the next rung, or run: deyroute tunnel restart <tunnel>"},
	B040: {B040, "Waterwall config is not valid JSON: {file}",
		"the rendered Waterwall file failed validation before start",
		"report this with deyroute doctor; the rung is skipped"},
	B041: {B041, "Waterwall core.json missing in {dir}",
		"Waterwall must start with WorkingDirectory set to the core.json folder",
		"run deyroute tunnel restart <tunnel> to re-render"},
	B042: {B042, "No decoy SNI is reachable",
		"none of {decoys} answered a TLS 1.3 handshake from the hub",
		"list reachable TLS 1.3 sites in hub.decoy_snis with: deyroute config edit"},
	B043: {B043, "Waterwall failed to start",
		"Waterwall exited; its debug log is shown below",
		"fix the cause shown in the log or remove the rung from the ladder"},
	B044: {B044, "Waterwall Reality handshake failed",
		"the node could not complete the Reality handshake with the hub",
		"check that the decoy SNI is reachable and the password matches on both sides (re-render)"},
	B060: {B060, "Relay config is invalid: {path}",
		"the direct/native relay could not use its rendered file ({reason})",
		"re-render the tunnel: deyroute tunnel restart <tunnel>; if it repeats run deyroute doctor"},
	B061: {B061, "Relay cannot listen on {addr} ({proto})",
		"{reason}",
		"free the port (ss -lntup shows the owner) or pick another listen port; deyroute port check <port> explains conflicts"},
	B062: {B062, "Service {target} is not reachable on the node's public address",
		"direct/haproxy connects from the hub straight to the node service, but nothing answered on {tried}; the service listens only on 127.0.0.1 or a firewall blocks it",
		"make the service listen on 0.0.0.0 and allow the hub IP in the node firewall, or use direct/native instead"},
	B070: {B070, "WireGuard interface {iface} could not be set up",
		"{reason}",
		"check that the wireguard kernel module loads (modprobe wireguard) and see the tunnel log (deyroute logs <tunnel>); or use awg/userspace, which needs no kernel module"},
	B071: {B071, "AmneziaWG interface {iface} could not be configured",
		"{reason}",
		"check that the awg unit runs (systemctl status 'deyroute-tun@*awg-userspace*') and read the tunnel log (deyroute logs <tunnel>)"},
	B072: {B072, "WireGuard config {path} is invalid",
		"{reason}",
		"re-render the tunnel (deyroute tunnel restart <tunnel>); if it repeats run deyroute doctor"},

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
	F007: {F007, "Tunnel {tunnel}: failover engine is not running",
		"the tunnel is disabled, or the hub service is starting or stopping",
		"check deyroute status; enable the tunnel (deyroute tunnel enable {tunnel}) or restart the hub: systemctl restart deyroute-hub"},
	F008: {F008, "Tunnel {tunnel}: {command} did not finish in time",
		"the request timed out or was cancelled while the failover engine was busy (switching or testing the ladder)",
		"check the current state with: deyroute tunnel show {tunnel}; then retry the command if needed"},

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
	S008: {S008, "A backup passphrase is required",
		"backups contain the CA key and tunnel secrets and are encrypted with age by default",
		"enter a passphrase, or run deyroute backup --no-encrypt and keep the file private"},
	S009: {S009, "Cannot use secret file {path}",
		"the file is missing, empty, unreadable or damaged: {reason}",
		"check the file (ls -l {path}); restore it from a backup (deyroute restore FILE) or recreate it through the menu"},

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
	X020: {X020, "State database is in use: {path}",
		"another deyroute process holds the database lock (only one hub or node daemon may run)",
		"stop the other process (systemctl stop deyroute-hub deyroute-node) or wait for it to exit, then retry"},
	X021: {X021, "State database operation failed: {op}",
		"bbolt returned an error for {path} (disk full, I/O error or a damaged record)",
		"check free space and disk health (df -h /var/lib/deyroute); if it persists run deyroute doctor"},
	X022: {X022, "Cannot write log file {path}",
		"the log directory is missing, not writable or the disk is full",
		"check permissions and free space: ls -ld /var/log/deyroute; df -h /var/log"},
	X030: {X030, "Program not installed: {command}",
		"deyroute needs {command} but it is not in PATH or the standard system directories",
		"install the distribution package that provides {command} (e.g. apt install iproute2 nftables), then retry"},
	X031: {X031, "External command did not finish: {command}",
		"it was stopped because its time limit was reached or deyroute was shutting down",
		"check system load (uptime) and the service log; retry the action"},
	X032: {X032, "Cannot write system file {path}",
		"the directory is missing or read-only, the disk is full, or deyroute is not running as root",
		"run deyroute as root and check free space and mounts: df -h; mount | grep ' / '"},
	X033: {X033, "Cannot change kernel setting {key}",
		"writing {value} to /proc/sys failed (read-only /proc/sys in a container, or a value this kernel rejects)",
		"run on the host as root (not in an unprivileged container); undo all tuning with: deyroute optimize revert"},
	X034: {X034, "Invalid systemd unit data: {field}='{value}'",
		"deyroute produced a unit name or setting that systemd would reject or misread",
		"this is a bug; run deyroute doctor and report the generated file"},
	X040: {X040, "Another deyroute daemon is already running",
		"the local API socket {path} is answered by another process (only one hub or node daemon may run)",
		"stop the other instance first: systemctl stop deyroute-hub deyroute-node; then start the service again"},
	X041: {X041, "Cannot open the local API socket {path}",
		"{reason}",
		"check that /run/deyroute is writable by root and the path is not used by another file, then: systemctl restart deyroute-hub"},
	X042: {X042, "The daemon did not finish {method} in time",
		"the call was cancelled or exceeded its time limit before the daemon answered",
		"check the daemon: systemctl status {service}; see deyroute logs hub; then try again"},
	X050: {X050, "Telegram message could not be delivered",
		"api.telegram.org was not reachable directly or through any online node ({reason})",
		"check outbound HTTPS (or https_proxy) on the hub and nodes, then: deyroute notify telegram test; events are still in: deyroute events"},
	X051: {X051, "Probe helper {service} on {addr} stopped",
		"accepting or reading on its socket failed unexpectedly",
		"restart the service (systemctl restart deyroute-node, or deyroute-hub on the hub); if it repeats run: deyroute doctor"},
	X052: {X052, "Speed test to {addr} failed during {phase}",
		"{reason}",
		"check the tunnel first: deyroute diag probe <tunnel>; then retry with a shorter test: deyroute diag speed <tunnel> --seconds 5"},
	X060: {X060, "Doctor file not written: {file} still contains {what}",
		"the final check found secret material that the central filter did not remove, so no file was created (nothing leaked)",
		"send the summary printed on the screen instead of the file and report this bug with the DEY code; logs are in /var/log/deyroute"},
}
