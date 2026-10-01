package cli

import (
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/version"
)

// --------------------------------------------------------------- optimize

func newOptimizeCmd(g *Globals) *cobra.Command {
	cmd := newGroup("optimize", i18n.CLIOptimizeShort)
	var profile string
	apply := &cobra.Command{
		Use:     "apply --profile balanced|aggressive|off",
		Short:   i18n.T(i18n.CLIOptimizeApplyShort),
		Long:    i18n.T(i18n.CLIOptimizeApplyLong),
		Example: i18n.T(i18n.CLIOptimizeApplyExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch profile {
			case config.SysctlBalanced, config.SysctlAggressive, config.SysctlOff:
			case "":
				return usageErr(i18n.T(i18n.CLIWantFlag, "--profile"))
			default:
				return deyerr.New(deyerr.C013, deyerr.Params{"field": "--profile", "value": profile, "allowed": "balanced, aggressive, off"})
			}
			var st api.OptimizeStatus
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				st, err = l.OptimizeApply(ctx, profile)
				return err
			})
			if err != nil {
				return err
			}
			return g.printOptimize(st, i18n.CLIOptimizeApplied)
		},
	}
	apply.Flags().StringVar(&profile, "profile", "", i18n.T(i18n.CLIFlagProfile))
	revert := &cobra.Command{
		Use:     "revert",
		Short:   i18n.T(i18n.CLIOptimizeRevertShort),
		Example: i18n.T(i18n.CLIOptimizeRevertExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var st api.OptimizeStatus
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				st, err = l.OptimizeRevert(ctx)
				return err
			})
			if err != nil {
				return err
			}
			return g.printOptimize(st, i18n.CLIOptimizeReverted)
		},
	}
	cmd.AddCommand(apply, revert)
	return cmd
}

func (g *Globals) printOptimize(st api.OptimizeStatus, k i18n.Key) error {
	if g.JSON {
		return g.emitJSON(st)
	}
	g.say(k, orDash(st.Profile))
	bbr := i18n.T(i18n.CLIBBRInactive)
	switch {
	case st.BBRActive:
		bbr = i18n.T(i18n.CLIBBRActive)
	case !st.BBRAvailable:
		bbr = i18n.T(i18n.CLIBBRMissing)
	}
	g.println(g.text("  " + bbr))
	keys := make([]string, 0, len(st.Applied))
	for key := range st.Applied {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		g.println(g.text("  " + key + " = " + st.Applied[key]))
	}
	for _, w := range st.Warnings {
		g.println(g.text("  " + g.sym().warn + " " + clean(w)))
	}
	return nil
}

// --------------------------------------------------------------- security

func newSecurityCmd(g *Globals) *cobra.Command {
	cmd := newGroup("security", i18n.CLISecurityShort)
	tls := newGroup("tls", i18n.CLISecurityTLSShort)
	tls.Example = i18n.T(i18n.CLISecurityTLSExample)
	tls.AddCommand(newTLSCmd(g, "show"), newTLSCmd(g, "renew"), newTLSDomainCmd(g), newTLSACMECmd(g))
	fw := newGroup("firewall", i18n.CLISecurityFirewallShort)
	fw.AddCommand(newFirewallCmd(g, "show"), newFirewallCmd(g, "apply"), newFirewallCmd(g, "disable"))
	cmd.AddCommand(newRotateTokensCmd(g), newRotateCACmd(g), tls, fw, newAuditCmd(g))
	return cmd
}

func newRotateTokensCmd(g *Globals) *cobra.Command {
	var tunnel string
	var yes bool
	cmd := &cobra.Command{
		Use:     "rotate-tokens",
		Short:   i18n.T(i18n.CLIRotateTokensShort),
		Example: i18n.T(i18n.CLIRotateTokensExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := g.local()
			if err != nil {
				return err
			}
			what := i18n.T(i18n.CLIAllTunnels)
			if tunnel != "" {
				what = i18n.T(i18n.CLIOneTunnel, tunnel)
			}
			if err := g.confirm(i18n.T(i18n.CLIRotateTokensLost, what), yes); err != nil {
				return err
			}
			p := g.newProgress()
			ctx, cancel := longCtx(cmd.Context())
			defer cancel()
			if err := l.SecurityRotateTokens(ctx, tunnel, p.step); err != nil {
				return err
			}
			if g.JSON {
				return g.ok(map[string]any{"tunnel": tunnel}, p.steps)
			}
			g.say(i18n.CLITokensRotated, what)
			return nil
		},
	}
	cmd.Flags().StringVar(&tunnel, "tunnel", "", i18n.T(i18n.CLIFlagRotateTunnel))
	cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

func newRotateCACmd(g *Globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rotate-ca",
		Short:   i18n.T(i18n.CLIRotateCAShort),
		Example: i18n.T(i18n.CLIRotateCAExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := g.local()
			if err != nil {
				return err
			}
			if err := g.confirm(i18n.T(i18n.CLIRotateCALost), yes); err != nil {
				return err
			}
			p := g.newProgress()
			ctx, cancel := longCtx(cmd.Context())
			defer cancel()
			r, err := l.SecurityRotateCA(ctx, p.step)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"reissued": nonNil(r.Reissued), "offline": nonNil(r.Offline), "steps": p.steps})
			}
			g.say(i18n.CLICARotated, orDash(strings.Join(r.Reissued, ", ")))
			if len(r.Offline) > 0 {
				g.say(i18n.CLICAOffline, strings.Join(r.Offline, ", "))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

func newTLSCmd(g *Globals, verb string) *cobra.Command {
	var tunnel string
	short, example := i18n.CLITLSShowShort, i18n.CLITLSShowExample
	if verb == "renew" {
		short, example = i18n.CLITLSRenewShort, i18n.CLITLSRenewExample
	}
	cmd := &cobra.Command{
		Use:     verb,
		Short:   i18n.T(short),
		Example: i18n.T(example),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var cs []api.CertInfo
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				if verb == "renew" {
					cs, err = l.SecurityTLSRenew(ctx, tunnel)
				} else {
					cs, err = l.SecurityTLSShow(ctx, tunnel)
				}
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"certificates": nonNil(cs)})
			}
			if verb == "renew" {
				g.say(i18n.CLITLSRenewed)
			}
			rows := make([][]string, 0, len(cs))
			for _, c := range cs {
				days := strconv.Itoa(c.DaysLeft)
				if c.Warning != "" {
					days += " " + g.sym().warn
				}
				rows = append(rows, []string{c.Kind, orDash(c.Tunnel), orDash(c.Mode), clean(c.Subject),
					localTime(c.NotAfter, "2006-01-02"), days, c.Fingerprint})
			}
			g.table([]string{i18n.T(i18n.CLIColKind), i18n.T(i18n.CLIColTunnel), i18n.T(i18n.CLIColMode), i18n.T(i18n.CLIColSubject),
				i18n.T(i18n.CLIColExpires), i18n.T(i18n.CLIColDays), i18n.T(i18n.CLIColFingerprint)}, rows)
			for _, c := range cs {
				if c.Warning != "" {
					g.println(g.text("  " + g.sym().warn + " " + c.Kind + " " + orDash(c.Tunnel) + ": " + clean(c.Warning)))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tunnel, "tunnel", "", i18n.T(i18n.CLIFlagTLSTunnel))
	return cmd
}

// newTLSDomainCmd is `deyroute security tls domain <name> | --clear`:
// hub.domain, the name tls.mode acme requests a Let's Encrypt certificate
// for (section 10). The tunnels are rendered again (TLS SANs).
func newTLSDomainCmd(g *Globals) *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use:     "domain <name> | --clear",
		Short:   i18n.T(i18n.CLITLSDomainShort),
		Long:    i18n.T(i18n.CLITLSDomainLong),
		Example: i18n.T(i18n.CLITLSDomainExample),
		Args:    rangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			domain := ""
			if len(args) == 1 {
				domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(args[0]), "."))
			}
			if remove == (domain != "") || len(args) == 1 && domain == "" {
				return usageErr(i18n.T(i18n.CLIWantDomainOrClear))
			}
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) error {
				return l.SettingsSet(ctx, api.SettingsRequest{Domain: &domain})
			})
			if err != nil {
				return err
			}
			if remove {
				return g.done(map[string]any{"domain": ""}, i18n.CLITLSDomainCleared)
			}
			return g.done(map[string]any{"domain": domain}, i18n.CLITLSDomainSet, domain)
		},
	}
	cmd.Flags().BoolVar(&remove, "clear", false, i18n.T(i18n.CLIFlagClearDomain))
	return cmd
}

// newTLSACMECmd is `deyroute security tls acme [--email E]
// [--cloudflare-token-file F]`: the ACME account e-mail and the Cloudflare
// API token for DNS-01 (section 10, Advanced). The daemon copies the token
// to /etc/deyroute/secrets/cloudflare.token (0600); an empty value removes
// the setting.
func newTLSACMECmd(g *Globals) *cobra.Command {
	var email, tokenFile string
	cmd := &cobra.Command{
		Use:     "acme [--email E] [--cloudflare-token-file F]",
		Short:   i18n.T(i18n.CLITLSACMEShort),
		Long:    i18n.T(i18n.CLITLSACMELong),
		Example: i18n.T(i18n.CLITLSACMEExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			fl := cmd.Flags()
			var req api.SettingsRequest
			out := map[string]any{}
			if fl.Changed("email") {
				e := strings.TrimSpace(email)
				req.ACMEEmail = &e
				out["email"] = e
			}
			if fl.Changed("cloudflare-token-file") {
				f := strings.TrimSpace(tokenFile)
				if f != "" {
					// The daemon reads the file with its own working
					// directory (/): a relative path is resolved here.
					abs, err := filepath.Abs(f)
					if err != nil {
						return deyerr.New(deyerr.C013, deyerr.Params{"field": "--cloudflare-token-file", "value": f, "allowed": i18n.T(i18n.CLIWantAbsPath)})
					}
					f = abs
				}
				req.CloudflareTokenFile = &f
				out["cloudflare_token_file"] = ""
				if f != "" {
					out["cloudflare_token_file"] = config.DefaultCloudflareTokenFile
				}
			}
			if req.ACMEEmail == nil && req.CloudflareTokenFile == nil {
				return usageErr(i18n.T(i18n.CLIACMENothing))
			}
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) error { return l.SettingsSet(ctx, req) })
			if err != nil {
				return err
			}
			if g.JSON {
				return g.ok(out, nil)
			}
			switch {
			case req.ACMEEmail == nil:
			case *req.ACMEEmail == "":
				g.say(i18n.CLIACMEEmailRemoved)
			default:
				g.say(i18n.CLIACMEEmailSet, *req.ACMEEmail)
			}
			switch {
			case req.CloudflareTokenFile == nil:
			case *req.CloudflareTokenFile == "":
				g.say(i18n.CLIACMETokenRemoved)
			default:
				g.say(i18n.CLIACMETokenSet, config.DefaultCloudflareTokenFile)
			}
			g.say(i18n.CLIACMERenewHint)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", i18n.T(i18n.CLIFlagACMEEmail))
	cmd.Flags().StringVar(&tokenFile, "cloudflare-token-file", "", i18n.T(i18n.CLIFlagCloudflareToken))
	return cmd
}

func newFirewallCmd(g *Globals, action string) *cobra.Command {
	var yes bool
	short, example := i18n.CLIFirewallShowShort, i18n.CLIFirewallShowExample
	switch action {
	case "apply":
		short, example = i18n.CLIFirewallApplyShort, i18n.CLIFirewallApplyExample
	case "disable":
		short, example = i18n.CLIFirewallDisableShort, i18n.CLIFirewallDisableExample
	}
	cmd := &cobra.Command{
		Use:     action,
		Short:   i18n.T(short),
		Example: i18n.T(example),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if action == "disable" {
				if err := g.confirm(i18n.T(i18n.TUISeFwDisableLost), yes); err != nil {
					return err
				}
			}
			var fi api.FirewallInfo
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				fi, err = l.SecurityFirewall(ctx, action)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(fi)
			}
			switch action {
			case "apply":
				g.say(i18n.CLIFirewallApplied)
			case "disable":
				g.say(i18n.CLIFirewallDisabled)
			}
			mode := i18n.T(i18n.CLIFirewallManaged)
			if !fi.Managed {
				mode = i18n.T(i18n.CLIFirewallSuggestOnly)
			}
			g.say(i18n.CLIFirewallMode, mode, orDash(strings.Join(fi.Detected, ", ")))
			if strings.TrimSpace(fi.Ruleset) != "" {
				g.println(g.text(clean(strings.TrimRight(fi.Ruleset, "\n"))))
			}
			if len(fi.Suggested) > 0 {
				g.say(i18n.CLIFirewallSuggested)
				for _, s := range fi.Suggested {
					g.println(g.text("  " + clean(s)))
				}
			}
			return nil
		},
	}
	if action == "disable" {
		cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	}
	return cmd
}

func newAuditCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:     "audit",
		Short:   i18n.T(i18n.CLIAuditShort),
		Example: i18n.T(i18n.CLIAuditExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var r api.AuditReport
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				r, err = l.SecurityAudit(ctx)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"clean": r.Clean, "items": nonNil(r.Items)})
			}
			s := g.sym()
			for _, it := range r.Items {
				mark := s.ok
				switch it.Severity {
				case "warn":
					mark = s.warn
				case "error":
					mark = s.fail
				}
				g.println(g.text("  " + mark + " " + pad(it.Check, 18) + clean(it.Message)))
			}
			if r.Clean {
				g.say(i18n.CLIAuditClean)
			} else {
				g.say(i18n.CLIAuditProblems)
			}
			return nil
		},
	}
}

// ----------------------------------------------------------------- notify

func newNotifyCmd(g *Globals) *cobra.Command {
	cmd := newGroup("notify", i18n.CLINotifyShort)
	tg := newGroup("telegram", i18n.CLITelegramShort)
	var tokenFile, chatID string
	set := &cobra.Command{
		Use:     "set --token-file F --chat-id C",
		Short:   i18n.T(i18n.CLITelegramSetShort),
		Long:    i18n.T(i18n.CLITelegramSetLong),
		Example: i18n.T(i18n.CLITelegramSetExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			tokenFile, chatID = strings.TrimSpace(tokenFile), strings.TrimSpace(chatID)
			if tokenFile == "" {
				return usageErr(i18n.T(i18n.CLIWantFlag, "--token-file"))
			}
			if chatID == "" {
				return usageErr(i18n.T(i18n.CLIWantFlag, "--chat-id"))
			}
			// The daemon reads the file with its own working directory
			// (/): a relative path is resolved here, where it was typed.
			abs, err := filepath.Abs(tokenFile)
			if err != nil {
				return deyerr.New(deyerr.C013, deyerr.Params{"field": "--token-file", "value": tokenFile, "allowed": i18n.T(i18n.CLIWantAbsPath)})
			}
			tokenFile = abs
			err = g.call(cmd.Context(), func(ctx context.Context, l api.Local) error {
				return l.NotifyTelegramSet(ctx, tokenFile, chatID, nil)
			})
			if err != nil {
				return err
			}
			return g.done(map[string]any{"token_file": tokenFile, "chat_id": chatID}, i18n.CLITelegramSet, chatID)
		},
	}
	set.Flags().StringVar(&tokenFile, "token-file", "", i18n.T(i18n.CLIFlagTokenFile))
	set.Flags().StringVar(&chatID, "chat-id", "", i18n.T(i18n.CLIFlagChatID))
	test := &cobra.Command{
		Use:     "test",
		Short:   i18n.T(i18n.CLITelegramTestShort),
		Example: i18n.T(i18n.CLITelegramTestExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) error { return l.NotifyTelegramTest(ctx) })
			if err != nil {
				return err
			}
			return g.done(nil, i18n.CLITelegramTested)
		},
	}
	off := &cobra.Command{
		Use:     "off",
		Short:   i18n.T(i18n.CLITelegramOffShort),
		Example: i18n.T(i18n.CLITelegramOffExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) error { return l.NotifyTelegramOff(ctx) })
			if err != nil {
				return err
			}
			return g.done(nil, i18n.CLITelegramOff)
		},
	}
	tg.AddCommand(set, test, off)
	cmd.AddCommand(tg)
	return cmd
}

// --------------------------------------------------------------- settings

func newSettingsCmd(g *Globals) *cobra.Command {
	cmd := newGroup("settings", i18n.CLISettingsShort)
	cmd.AddCommand(&cobra.Command{
		Use:       "ui-mode simple|advanced",
		Short:     i18n.T(i18n.CLIUIModeShort),
		Example:   i18n.T(i18n.CLIUIModeExample),
		Args:      exactArgs(1),
		ValidArgs: []string{config.UIModeSimple, config.UIModeAdvanced},
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := strings.ToLower(strings.TrimSpace(args[0]))
			if mode != config.UIModeSimple && mode != config.UIModeAdvanced {
				return deyerr.New(deyerr.C013, deyerr.Params{"field": "ui_mode", "value": args[0], "allowed": "simple, advanced"})
			}
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) error {
				return l.SettingsSet(ctx, api.SettingsRequest{UIMode: mode})
			})
			if err != nil {
				return err
			}
			return g.done(map[string]any{"ui_mode": mode}, i18n.CLIUIModeSet, mode)
		},
	})
	return cmd
}

// ----------------------------------------------------------------- update

// rollbackLocal is `deyroute update --rollback` without the daemon (it is
// down, or this is a node): the binary and deyroute.prev are swapped here
// and the role's service is restarted.
func (g *Globals) rollbackLocal(ctx context.Context, yes bool) error {
	if err := g.confirm(i18n.T(i18n.CLIRollbackLost), yes); err != nil {
		return err
	}
	if err := g.Ops.SelfRollback(g.Root); err != nil {
		return err
	}
	unit := g.service() + ".service"
	rctx, cancel := longCtx(ctx)
	defer cancel()
	if _, _, err := g.Runner.Run(rctx, "systemctl", []string{"restart", unit}, nil); err != nil {
		g.note(i18n.CLIRollbackRestartFailed, unit)
		return err
	}
	return g.done(map[string]any{"rolled_back": true, "daemon_running": false, "service": unit}, i18n.CLIRolledBackLocal, unit)
}

func newUpdateCmd(g *Globals) *cobra.Command {
	var check, rollback, yes bool
	var ver string
	cmd := &cobra.Command{
		Use:     "update",
		Short:   i18n.T(i18n.CLIUpdateShort),
		Long:    i18n.T(i18n.CLIUpdateLong),
		Example: i18n.T(i18n.CLIUpdateExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if check && (rollback || ver != "") || rollback && ver != "" {
				return usageErr(i18n.T(i18n.CLIUpdateFlagsConflict))
			}
			l, err := g.local()
			if rollback && (errDaemonDown(err) || err == nil && g.role() == config.RoleNode) {
				// A crash-looping daemon after a bad update must still be
				// rolled back, and a node has no update API of its own.
				return g.rollbackLocal(cmd.Context(), yes)
			}
			if err != nil {
				return err
			}
			switch {
			case check:
				ctx, cancel := callCtx(cmd.Context())
				defer cancel()
				info, err := l.UpdateCheck(ctx)
				if err != nil {
					return err
				}
				return g.printUpdateInfo(info)
			case rollback:
				if err := g.confirm(i18n.T(i18n.CLIRollbackLost), yes); err != nil {
					return err
				}
				ctx, cancel := longCtx(cmd.Context())
				defer cancel()
				info, err := l.UpdateRollback(ctx)
				if err != nil {
					return err
				}
				if g.JSON {
					return g.emitJSON(info)
				}
				g.say(i18n.CLIRolledBack, orDash(info.Current))
				return nil
			}
			ctx, cancel := longCtx(cmd.Context())
			defer cancel()
			target := strings.TrimPrefix(strings.TrimSpace(ver), "v")
			info, err := l.UpdateCheck(ctx)
			switch {
			case err != nil && (target == "" || errDaemonDown(err)):
				return err
			case err != nil:
				// An explicit --version does not depend on the release
				// check: the hub may not reach the release API while the
				// mirror or a node can still fetch that version.
				g.note(i18n.CLIUpdateCheckSkipped, deyerr.As(err).Code, target)
				info = api.UpdateInfo{Current: version.Version}
			}
			if target == "" {
				if !info.Available {
					if g.JSON {
						return g.emitJSON(info)
					}
					g.say(i18n.CLIUpToDate, orDash(info.Current))
					return nil
				}
				target = info.Latest
			}
			if !g.JSON && info.Changelog != "" && target == info.Latest {
				g.say(i18n.CLIChangelog, info.Latest)
				g.println(g.text(clean(strings.TrimRight(info.Changelog, "\n"))))
			}
			if err := g.confirm(i18n.T(i18n.CLIUpdateLost, orDash(info.Current), target), yes); err != nil {
				return err
			}
			p := g.newProgress()
			res, err := l.UpdateApply(ctx, target, p.step)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"update": res, "steps": p.steps})
			}
			g.say(i18n.CLIUpdated, orDash(res.Previous), orDash(res.Current))
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&check, "check", false, i18n.T(i18n.CLIFlagCheck))
	f.StringVar(&ver, "version", "", i18n.T(i18n.CLIFlagVersion))
	f.BoolVar(&rollback, "rollback", false, i18n.T(i18n.CLIFlagRollback))
	f.BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	cmd.AddCommand(newUpdateBackendsCmd(g), newUpdateManifestCmd(g))
	return cmd
}

func (g *Globals) printUpdateInfo(info api.UpdateInfo) error {
	if g.JSON {
		return g.emitJSON(info)
	}
	if !info.Available {
		g.say(i18n.CLIUpToDate, orDash(info.Current))
		return nil
	}
	g.say(i18n.CLIUpdateAvailable, orDash(info.Current), info.Latest)
	if info.Changelog != "" {
		g.println(g.text(clean(strings.TrimRight(info.Changelog, "\n"))))
	}
	return nil
}

func newUpdateBackendsCmd(g *Globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "backends [name]",
		Short:   i18n.T(i18n.CLIUpdateBackendsShort),
		Example: i18n.T(i18n.CLIUpdateBackendsExample),
		Args:    rangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			l, err := g.local()
			if err != nil {
				return err
			}
			what := i18n.T(i18n.CLIAllBackends)
			if name != "" {
				what = name
			}
			if err := g.confirm(i18n.T(i18n.CLIUpdateBackendsLost, what), yes); err != nil {
				return err
			}
			p := g.newProgress()
			ctx, cancel := longCtx(cmd.Context())
			defer cancel()
			res, err := l.UpdateBackends(ctx, name, p.step)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"backends": nonNil(res), "steps": p.steps})
			}
			rows := make([][]string, 0, len(res))
			for _, b := range res {
				detail := ""
				if b.Error != nil {
					detail = b.Error.Code + " " + clean(b.Error.Message)
				}
				rows = append(rows, []string{b.Backend, orDash(b.From), orDash(b.To), b.Status, detail})
			}
			g.table([]string{i18n.T(i18n.CLIColBackend), i18n.T(i18n.CLIColFrom), i18n.T(i18n.CLIColTo),
				i18n.T(i18n.CLIColResult), i18n.T(i18n.CLIColDetail)}, rows)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

func newUpdateManifestCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:     "manifest",
		Short:   i18n.T(i18n.CLIUpdateManifestShort),
		Example: i18n.T(i18n.CLIUpdateManifestExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var m api.ManifestInfo
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				m, err = l.UpdateManifest(ctx)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(m)
			}
			g.say(i18n.CLIManifestUpdated, orDash(m.Source))
			names := make([]string, 0, len(m.Versions))
			for n := range m.Versions {
				names = append(names, n)
			}
			sort.Strings(names)
			rows := make([][]string, 0, len(names))
			for _, n := range names {
				rows = append(rows, []string{n, m.Versions[n]})
			}
			g.table([]string{i18n.T(i18n.CLIColBackend), i18n.T(i18n.CLIColVersion)}, rows)
			return nil
		},
	}
}
