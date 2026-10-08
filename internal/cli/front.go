package cli

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/front"
	"github.com/localroot4/deyroute/internal/i18n"
)

// defaultFrontPort is the Cloudflare port `front enable` uses without
// --port: an HTTPS port that is not 443 (443 is the first port tunnels get).
const defaultFrontPort = 2053

// newFrontCmd is `deyroute front enable|status|disable`: the hub's CDN
// front (hub.front), so nodes whose direct path is cut reach the hub, and
// carry their tunnels, through Cloudflare.
func newFrontCmd(g *Globals) *cobra.Command {
	cmd := newGroup("front", i18n.CLIFrontShort)
	cmd.Long = i18n.T(i18n.CLIFrontLong)

	var domain, tlsMode string
	var port int
	enable := &cobra.Command{
		Use:     "enable --domain <name> [--port 2053]",
		Short:   i18n.T(i18n.CLIFrontEnableShort),
		Example: i18n.T(i18n.CLIFrontEnableExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if domain == "" {
				return usageErr(i18n.T(i18n.CLIWantFlag, "--domain"))
			}
			if port == 0 {
				port = defaultFrontPort
			}
			mode := tlsMode
			if mode == "" && config.FrontSchemeForPort(port) == config.FrontSchemeWS {
				mode = config.FrontTLSOff // Cloudflare speaks plain HTTP to the origin on its HTTP ports
			}
			err := g.editHubConfig(func(c *config.Config) {
				c.Hub.Front.Enabled = true
				c.Hub.Front.Domain = strings.ToLower(strings.TrimSpace(domain))
				c.Hub.Front.Port = port
				c.Hub.Front.TLS = mode
			})
			if err != nil {
				return err
			}
			return g.frontApplyAndShow(cmd.Context())
		},
	}
	enable.Flags().StringVar(&domain, "domain", "", i18n.T(i18n.CLIFlagFrontDomain))
	enable.Flags().IntVar(&port, "port", 0, i18n.T(i18n.CLIFlagFrontPort))
	enable.Flags().StringVar(&tlsMode, "tls", "", i18n.T(i18n.CLIFlagFrontTLS))

	status := &cobra.Command{
		Use:     "status",
		Short:   i18n.T(i18n.CLIFrontStatusShort),
		Example: i18n.T(i18n.CLIFrontStatusExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return g.frontShow(cmd.Context())
		},
	}

	disable := &cobra.Command{
		Use:     "disable",
		Short:   i18n.T(i18n.CLIFrontDisableShort),
		Example: i18n.T(i18n.CLIFrontDisableExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.hubConfig()
			if err != nil {
				return err
			}
			err = g.editHubConfig(func(c *config.Config) { c.Hub.Front.Enabled = false })
			if err != nil {
				return err
			}
			l, err := g.local()
			if err != nil {
				return err
			}
			if err := g.configApply(cmd.Context(), l, map[string]any{"front": false}); err != nil {
				return err
			}
			if !g.JSON {
				direct := net.JoinHostPort(cfg.Hub.PublicIP, strconv.Itoa(cfg.Hub.ControlPort))
				for _, n := range cfg.Nodes {
					if n.Route == config.RouteFront {
						g.say(i18n.CLIFrontDisabledNode, n.ID, direct)
					}
				}
			}
			return nil
		},
	}
	cmd.AddCommand(enable, status, disable)
	return cmd
}

// hubConfig loads config.yaml and checks that this server is the hub.
func (g *Globals) hubConfig() (*config.Config, error) {
	data, err := g.readConfig()
	if err != nil {
		return nil, err
	}
	c, err := config.ParseWith(data, validateOptions())
	if err != nil {
		return nil, err
	}
	if c.Role != config.RoleHub || c.Hub == nil {
		return nil, usageErr(i18n.T(i18n.CLIFrontHubOnly))
	}
	return c, nil
}

// editHubConfig changes config.yaml with edit, validated before it is
// written; `config apply` makes the hub use it.
func (g *Globals) editHubConfig(edit func(c *config.Config)) error {
	c, err := g.hubConfig()
	if err != nil {
		return err
	}
	edit(c)
	c.ApplyDefaults()
	if err := c.Validate(validateOptions()); err != nil {
		return err
	}
	return config.SaveWith(g.configPath(), c, validateOptions())
}

// frontApplyAndShow applies config.yaml and prints the front and what to do
// next.
func (g *Globals) frontApplyAndShow(ctx context.Context) error {
	l, err := g.local()
	if err != nil {
		return err
	}
	if g.JSON {
		return g.configApply(ctx, l, map[string]any{"front": true})
	}
	if err := g.configApply(ctx, l, nil); err != nil {
		return err
	}
	g.println()
	return g.frontShow(ctx)
}

// frontShow prints the front's state, the Cloudflare settings it needs and
// the command that moves a node onto it.
func (g *Globals) frontShow(ctx context.Context) error {
	cfg, err := g.hubConfig()
	if err != nil {
		return err
	}
	f := cfg.Hub.Front
	var st *api.FrontStatus
	if l, err := g.local(); err == nil {
		if s, err := l.Status(ctx); err == nil {
			st = s.Hub.Front
		}
	}
	listening := st != nil && st.Listening
	var nodes []string
	for _, n := range cfg.Nodes {
		if n.Route == config.RouteFront {
			nodes = append(nodes, n.ID)
		}
	}
	target := ""
	if f.Enabled {
		if secret, err := g.frontSecret(f); err == nil {
			scheme := config.FrontSchemeForPort(f.Port)
			target = scheme + "://" + net.JoinHostPort(f.Domain, strconv.Itoa(f.Port)) + "/" + secret
		}
	}
	if g.JSON {
		// The node command carries the path secret: it is shown to the owner on
		// the hub, like a join command, and never logged.
		return g.emitJSON(map[string]any{
			"enabled": f.Enabled, "domain": f.Domain, "port": f.Port, "tls": f.TLSMode(),
			"listening": listening, "nodes": nonNil(nodes), "node_target": target,
		})
	}
	if !f.Enabled {
		g.say(i18n.CLIFrontOff)
		return nil
	}
	state := i18n.T(i18n.CLIFrontListening)
	if !listening {
		state = i18n.T(i18n.CLIFrontNotListening)
	}
	g.say(i18n.CLIFrontOn, f.Domain, f.Port, state, f.TLSMode())
	if len(nodes) > 0 {
		g.say(i18n.CLIFrontNodes, strings.Join(nodes, ", "))
	}
	g.println()
	g.say(i18n.CLIFrontCloudflare, f.Domain, cfg.Hub.PublicIP, cloudflareSSL(f))
	if target != "" {
		g.println()
		g.say(i18n.CLIFrontNodeCommand)
		g.println("   deyroute node set-hub '" + target + "'")
	}
	return nil
}

// cloudflareSSL names the Cloudflare SSL/TLS mode the front's TLS needs.
func cloudflareSSL(f config.HubFront) string {
	if f.TLSMode() == config.FrontTLSOff {
		return "Flexible"
	}
	return "Full"
}

// frontSecret reads the front path secret the hub created (root only).
func (g *Globals) frontSecret(f config.HubFront) (string, error) {
	data, err := os.ReadFile(g.path(filepath.Join(config.SecretsDir, f.SecretFileOrDefault())))
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(data))
	if !front.ValidSecret(s) {
		return "", deyerr.New(deyerr.T008, deyerr.Params{"path": config.SecretsDir, "reason": "not a front secret"})
	}
	return s, nil
}
