package setup

import (
	"path/filepath"
	"strings"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// SetHubAddr points this node at a moved hub (`deyroute node set-hub
// <ip:port>`, spec section 5): it validates addr as host:port (DEY-C013)
// and updates node.hub_addr in Root/etc/deyroute/config.yaml atomically
// (config.Mutate). The node daemon calls it for set-hub/announce-move, the
// CLI when the daemon is down; the daemon reads the address again on its
// next reconnect. On a hub config it returns DEY-X009.
func SetHubAddr(root, addr string) error {
	if root == "" {
		root = "/"
	}
	addr = strings.TrimSpace(addr)
	if !config.ValidHostPort(addr) {
		return deyerr.New(deyerr.C013, deyerr.Params{
			"field": "node.hub_addr", "value": addr, "allowed": "host:port of the hub, e.g. 5.6.7.8:44433",
		})
	}
	_, err := config.Mutate(filepath.Join(root, config.DefaultPath), validateOptions(), func(c *config.Config) error {
		if c.Role != config.RoleNode || c.Node == nil {
			return deyerr.New(deyerr.X009, deyerr.Params{"role": c.Role, "need": config.RoleNode})
		}
		c.Node.HubAddr = addr
		return nil
	})
	return err
}
