package waterwall

import (
	"net"

	"github.com/localroot4/deyroute/internal/backend"
)

// wwConfig is one Waterwall node-graph file.
type wwConfig struct {
	Name  string   `json:"name"`
	Nodes []wwNode `json:"nodes"`
}

// wwNode is one node of the graph. Settings maps are encoded with sorted
// keys by encoding/json, so the output is deterministic.
type wwNode struct {
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Settings map[string]any `json:"settings"`
	Next     string         `json:"next,omitempty"`
}

// Node names used in both graphs.
const (
	nUsersInbound   = "users-inbound"
	nHeaderClient   = "header-client"
	nBridgeUsers    = "bridge-users"
	nBridgeReverse  = "bridge-reverse"
	nReverseServer  = "reverse-server"
	nRealityServer  = "reality-server"
	nNodeInbound    = "node-inbound"
	nRealityDecoy   = "reality-decoy"
	nServiceOut     = "service-outbound"
	nHeaderServer   = "header-server"
	nBridgeService  = "bridge-service"
	nReverseClient  = "reverse-client"
	nRealityClient  = "reality-client"
	nHubOutbound    = "hub-outbound"
	portFromContext = "dest_context->port"
)

// hubGraph is the hub side:
//
//	users-inbound (TcpListener user ports) [-> header-client] -> bridge-users
//	  <=pair=> bridge-reverse <- reverse-server <- reality-server
//	  <- node-inbound (TcpListener control port)
//	reality-server.destination = reality-decoy (TcpConnector decoy:443)
func hubGraph(in backend.RenderInput, p planned) []wwNode {
	var port any = p.listen[0]
	if p.multi {
		ports := make([]any, len(p.listen))
		for i, l := range p.listen {
			ports[i] = l
		}
		port = ports
	}
	usersNext := nBridgeUsers
	if p.multi {
		usersNext = nHeaderClient
	}
	nodes := []wwNode{{
		Name: nUsersInbound, Type: "TcpListener",
		Settings: map[string]any{"address": p.listenAddr, "port": port, "nodelay": true},
		Next:     usersNext,
	}}
	if p.multi {
		// The accepted listener port travels to the node in a 2-byte header.
		nodes = append(nodes, wwNode{
			Name: nHeaderClient, Type: "HeaderClient",
			Settings: map[string]any{"data": "src_context->port"},
			Next:     nBridgeUsers,
		})
	}
	control := map[string]any{"address": "0.0.0.0", "port": in.ControlPort, "nodelay": true}
	if wl := whitelist(in.Node.PublicIP); wl != "" {
		control["whitelist"] = []string{wl}
	}
	nodes = append(nodes,
		wwNode{Name: nBridgeUsers, Type: "Bridge", Settings: map[string]any{"pair": nBridgeReverse}},
		wwNode{Name: nBridgeReverse, Type: "Bridge", Settings: map[string]any{"pair": nBridgeUsers}},
		wwNode{Name: nReverseServer, Type: "ReverseServer", Settings: map[string]any{}, Next: nBridgeReverse},
		wwNode{
			Name: nRealityServer, Type: "RealityServer",
			Settings: map[string]any{"destination": nRealityDecoy, "password": p.password},
			Next:     nReverseServer,
		},
		wwNode{Name: nNodeInbound, Type: "TcpListener", Settings: control, Next: nRealityServer},
		wwNode{
			Name: nRealityDecoy, Type: "TcpConnector",
			Settings: map[string]any{"address": p.decoyHost, "port": p.decoyPort, "nodelay": true},
		},
	)
	return nodes
}

// nodeGraph is the node side:
//
//	bridge-service [-> header-server] -> service-outbound (TcpConnector target)
//	bridge-reverse -> reverse-client -> reality-client -> hub-outbound
//	  (TcpConnector hub:<ctl>)
func nodeGraph(in backend.RenderInput, p planned) []wwNode {
	var port any = p.port
	serviceIn := nServiceOut
	if p.multi {
		port = portFromContext
		serviceIn = nHeaderServer
	}
	nodes := []wwNode{{
		Name: nServiceOut, Type: "TcpConnector",
		Settings: map[string]any{"address": p.host, "port": port, "nodelay": true},
	}}
	if p.multi {
		nodes = append(nodes, wwNode{
			Name: nHeaderServer, Type: "HeaderServer",
			Settings: map[string]any{"override": portFromContext},
			Next:     nServiceOut,
		})
	}
	nodes = append(nodes,
		wwNode{Name: nBridgeService, Type: "Bridge", Settings: map[string]any{"pair": nBridgeReverse}, Next: serviceIn},
		wwNode{Name: nBridgeReverse, Type: "Bridge", Settings: map[string]any{"pair": nBridgeService}, Next: nReverseClient},
		wwNode{Name: nReverseClient, Type: "ReverseClient", Settings: map[string]any{"minimum-unused": minimumUnused}, Next: nRealityClient},
		wwNode{
			Name: nRealityClient, Type: "RealityClient",
			Settings: map[string]any{"sni": p.decoyHost, "password": p.password},
			Next:     nHubOutbound,
		},
		wwNode{
			Name: nHubOutbound, Type: "TcpConnector",
			Settings: map[string]any{"address": in.Hub.PublicIP, "port": in.ControlPort, "nodelay": true},
		},
	)
	return nodes
}

// whitelist returns "<ip>/32" (or /128) for the node's public IP, or "" when
// it is not an IP literal. The firewall already limits the control port to
// the node addresses; the listener filter mirrors the official example.
func whitelist(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	if parsed.To4() != nil {
		return parsed.String() + "/32"
	}
	return parsed.String() + "/128"
}

// coreFile is core.json (docs/01-getting-started/tutorial-part1 of the
// pinned version). Struct order keeps the output stable.
type coreFile struct {
	Log     coreLog  `json:"log"`
	Misc    coreMisc `json:"misc"`
	Configs []string `json:"configs"`
}

type coreLog struct {
	Path     string    `json:"path"`
	Internal logTarget `json:"internal"`
	Core     logTarget `json:"core"`
	Network  logTarget `json:"network"`
	DNS      logTarget `json:"dns"`
}

type logTarget struct {
	LogLevel string `json:"loglevel"`
	File     string `json:"file"`
	Console  bool   `json:"console"`
}

type coreMisc struct {
	Workers    int    `json:"workers"`
	RAMProfile string `json:"ram-profile"`
	MTU        int    `json:"mtu"`
	// deyroute owns kernel tuning (spec section 12); Waterwall must not
	// change sysctls on its own.
	TCPTune        bool `json:"tcp-tune"`
	TryEnablingBBR bool `json:"try-enabling-bbr"`
}

// CoreJSON returns core.json for the given worker count with steady-state
// logging (INFO).
func CoreJSON(workers int) []byte { return CoreJSONFor(workers, false) }

// CoreJSONFor returns core.json: "ram-profile": "server", the worker count
// (callers pass Workers(runtime.NumCPU()); values < 1 become 1), every
// logger at DEBUG on the first run (spec 7.4, Waterwall explains failures
// only at that level) and INFO afterwards, and configs = [config.json].
func CoreJSONFor(workers int, firstRun bool) []byte {
	if workers < 1 {
		workers = 1
	}
	level := "INFO"
	if firstRun {
		level = "DEBUG"
	}
	target := func(file string) logTarget { return logTarget{LogLevel: level, File: file, Console: true} }
	data, err := backend.JSONIndent(coreFile{
		Log: coreLog{
			Path:     logDir,
			Internal: target("internal.log"),
			Core:     target("core.log"),
			Network:  target("network.log"),
			DNS:      target("dns.log"),
		},
		Misc:    coreMisc{Workers: workers, RAMProfile: "server", MTU: mtu},
		Configs: []string{ConfigFile},
	})
	if err != nil {
		// Unreachable: the struct holds only strings, ints and bools.
		return nil
	}
	return data
}
