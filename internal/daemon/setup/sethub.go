package setup

import (
	"os"
	"path/filepath"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// SetHubAddr points this node at its hub (`deyroute node set-hub <target>`,
// spec section 5). It is the OWNER's command; the daemon calls it for the
// owner's local set-hub, the CLI when the daemon is down. The daemon never
// calls it for a hub-originated move of a front node (see the node agent).
//
// target is parsed with ParseHubTarget (DEY-C013):
//
//   - host:port (direct): node.hub_addr is updated and front mode is
//     CLEARED (node.front is removed together with its secret file), so the
//     node connects straight to the hub again.
//   - ws://DOMAIN:PORT/SECRET or wss://DOMAIN:PORT/SECRET (front): the
//     secret is written to Root/etc/deyroute/secrets/front.secret (0600),
//     node.hub_addr becomes DOMAIN:PORT and node.front is set, which
//     switches the node into front mode. A configured node.front.edge_ip is
//     kept.
//
// The file is updated atomically (config.Mutate); the daemon reads the
// address again on its next reconnect. On a hub config it returns DEY-X009.
func SetHubAddr(root, target string) error {
	if root == "" {
		root = "/"
	}
	t, err := ParseHubTarget(target)
	if err != nil {
		return err
	}
	if t.Front {
		dlog.RegisterSecret(t.Secret)
	}
	var stale string // a secret file that is no longer used
	_, err = config.Mutate(filepath.Join(root, config.DefaultPath), validateOptions(), func(c *config.Config) error {
		if c.Role != config.RoleNode || c.Node == nil {
			return deyerr.New(deyerr.X009, deyerr.Params{"role": c.Role, "need": config.RoleNode})
		}
		old := c.Node.Front
		c.Node.HubAddr = t.Addr
		if !t.Front {
			c.Node.Front = config.NodeFront{}
			stale = old.SecretFile
			return nil
		}
		if err := writeFrontSecret(root, config.DefaultFrontSecretFile, t.Secret); err != nil {
			return err
		}
		c.Node.Front = config.NodeFront{SecretFile: config.DefaultFrontSecretFile, Scheme: t.StoredScheme(), EdgeIP: old.EdgeIP}
		if old.SecretFile != config.DefaultFrontSecretFile {
			stale = old.SecretFile
		}
		return nil
	})
	if err == nil && stale != "" {
		// Best effort: a leftover file is harmless, nothing refers to it.
		_ = os.Remove(filepath.Join(root, config.SecretsDir, stale))
	}
	return err
}

// writeFrontSecret stores the front path secret as name below the secrets
// directory (0600, atomically).
func writeFrontSecret(root, name, secret string) error {
	return tlsutil.WriteSecret(filepath.Join(root, config.SecretsDir, name), []byte(secret+"\n"))
}
