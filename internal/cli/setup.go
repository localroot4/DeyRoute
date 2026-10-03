package cli

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/ports"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/sysinfo"
	"github.com/localroot4/deyroute/internal/tui"
	"github.com/localroot4/deyroute/internal/version"
)

// LocalOpTimeout bounds setup, join, restore and uninstall (they wait up to
// 20 s for the service socket and run systemctl/nft several times).
const LocalOpTimeout = 15 * time.Minute

// setupFlags are the flags of `deyroute setup`.
type setupFlags struct {
	role, name string
	port       int
	yes        bool
	repair     bool
}

func newSetupCmd(g *Globals) *cobra.Command {
	var f setupFlags
	cmd := &cobra.Command{
		Use:     "setup",
		Short:   i18n.T(i18n.CLISetupShort),
		Long:    i18n.T(i18n.CLISetupLong),
		Example: i18n.T(i18n.CLISetupExample),
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), LocalOpTimeout)
			defer cancel()
			if f.repair {
				return g.repair(ctx)
			}
			return g.setup(ctx, f)
		},
	}
	cmd.Flags().StringVar(&f.role, "role", "", i18n.T(i18n.CLIFlagRole))
	cmd.Flags().StringVar(&f.name, "name", "", i18n.T(i18n.CLIFlagSetupName))
	cmd.Flags().IntVar(&f.port, "control-port", 0, i18n.T(i18n.CLIFlagControlPort))
	cmd.Flags().BoolVar(&f.yes, "yes", false, i18n.T(i18n.CLIFlagYesSetup))
	cmd.Flags().BoolVar(&f.repair, "repair", false, i18n.T(i18n.CLIFlagRepair))
	_ = cmd.Flags().MarkHidden("repair")
	return cmd
}

func newJoinCmd(g *Globals) *cobra.Command {
	var name string
	var yes bool
	cmd := &cobra.Command{
		Use:     "join 'dey://TOKEN@HUB_IP:PORT#FP'",
		Short:   i18n.T(i18n.CLIJoinShort),
		Long:    i18n.T(i18n.CLIJoinLong),
		Example: i18n.T(i18n.CLIJoinExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), LocalOpTimeout)
			defer cancel()
			if g.configured() {
				return deyerr.New(deyerr.I013, deyerr.Params{"role": g.role()})
			}
			apply := true
			if g.IsTTY && !yes {
				ans, err := g.askQ(kernelQuestion())
				if err != nil {
					return err
				}
				apply = ans == answerYes
			}
			return g.join(ctx, args[0], name, apply)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", i18n.T(i18n.CLIFlagJoinName))
	cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYesSetup))
	_ = cmd.Flags().MarkHidden("yes")
	return cmd
}

// repair is `deyroute setup --repair`, run by install.sh on a server that
// is set up: unit files, layout and a restart of the role's service; the
// config is untouched (setup.Repair).
func (g *Globals) repair(ctx context.Context) error {
	unit, err := g.Ops.Repair(ctx, setup.RepairOptions{
		Root: g.Root, Runner: g.Runner, SocketPath: g.Socket, Logger: g.logger(),
		LookupUser: g.LookupUser, LookupGroup: g.LookupGroup, Chown: g.Chown,
	})
	if err != nil {
		return err
	}
	return g.done(map[string]any{"service": unit, "restarted": true}, i18n.CLISetupRepaired, unit)
}

// setup is the wizard of spec section 5: at most five questions for a hub
// (role, name, public IP, control port, automatic tuning), everything else
// automatic; a node asks for the join link and joins.
func (g *Globals) setup(ctx context.Context, f setupFlags) error {
	if g.configured() {
		return deyerr.New(deyerr.I013, deyerr.Params{"role": g.role()})
	}
	interactive := g.IsTTY && !f.yes
	if interactive {
		// The wizard opens with the DEYROUTE banner (section 6: every page).
		fmt.Fprintln(g.promptOut(), tui.Banner(g.outCaps(), tui.BannerStatus{Setup: true}))
	}
	role := strings.ToLower(strings.TrimSpace(f.role))
	switch role {
	case "", config.RoleHub, config.RoleNode:
	default:
		return deyerr.New(deyerr.C013, deyerr.Params{"field": "--role", "value": f.role, "allowed": "hub, node"})
	}
	st := &stepper{}
	if role == "" {
		switch {
		case interactive:
			g.note(i18n.CLISetupWelcome)
			// The role decides the other questions: count the hub's (the
			// common case) and renumber for a node after the answer.
			st.total = 1 + hubQuestions(f)
			ans, err := g.askQ(st.next(question{
				title: i18n.T(i18n.CLIAskRole),
				help:  []string{i18n.T(i18n.CLIRoleHelp)},
				opts: []askOpt{
					{value: config.RoleHub, label: i18n.T(i18n.CLIRoleHubLabel)},
					{value: config.RoleNode, label: i18n.T(i18n.CLIRoleNodeLabel)},
				},
				def: config.RoleHub,
			}))
			if err != nil {
				return err
			}
			role = ans
			if role == config.RoleNode {
				st.total = 1 + nodeQuestions(f)
			}
		case f.yes:
			role = config.RoleHub
		default:
			return deyerr.New(deyerr.I014, deyerr.Params{"step": "wizard"}).
				WithWhy(i18n.T(i18n.CLISetupNoTTYWhy)).WithFix(i18n.T(i18n.CLISetupNoTTYFix))
		}
	}
	if interactive && st.total == 0 {
		// --role given: only the questions of that role.
		st.total = hubQuestions(f)
		if role == config.RoleNode {
			st.total = nodeQuestions(f)
		}
	}
	if role == config.RoleNode {
		return g.setupNode(ctx, f, interactive, st)
	}
	return g.setupHub(ctx, f, interactive, st)
}

// hubQuestions is the number of questions the hub wizard asks after the
// role: name (unless --name), public IP, control port (unless
// --control-port) and automatic tuning.
func hubQuestions(f setupFlags) int {
	n := 2
	if strings.TrimSpace(f.name) == "" {
		n++
	}
	if f.port == 0 {
		n++
	}
	return n
}

// nodeQuestions is the number of questions of the node wizard: the join
// link, the name (unless --name) and the kernel profile.
func nodeQuestions(f setupFlags) int {
	if strings.TrimSpace(f.name) == "" {
		return 3
	}
	return 2
}

// kernelQuestion asks a node whether the balanced kernel profile is
// applied at join. A hub that uses automatic tuning tunes the node the same
// way once it is online (the join answer does not carry the hub's tuning).
func kernelQuestion() question {
	return question{
		title: i18n.T(i18n.CLIAskKernel),
		help:  []string{i18n.T(i18n.CLIKernelHelp), i18n.T(i18n.CLIKernelNodeAuto), i18n.T(i18n.CLIKernelUndo)},
		def:   answerYes, yesNo: true,
	}
}

// tunePreview computes the automatic tuning plan of this server in-process
// (the daemon does not run yet): the measured facts, the live values below
// Root and the ports the hub reserves (the backend control range and the
// control port). It changes nothing.
func (g *Globals) tunePreview(controlPort int) (sysinfo.Facts, sysctl.Plan) {
	f := g.HostFacts()
	reserved := []string{fmt.Sprintf("%d-%d", config.CtlRangeLow, config.CtlRangeHigh)}
	if controlPort > 0 {
		reserved = append(reserved, strconv.Itoa(controlPort))
	}
	return f, sysctl.AutoPlan(f, sysctl.AutoInputs{
		BBR: config.DefaultTuning().BBR, Conntrack: true, Reserved: reserved, Live: sysctl.Manager{Root: g.Root}.Live,
	})
}

// autoTuneQuestion is the hub wizard's last question: whether this server
// is tuned automatically, with the plan listed first (what was measured,
// every key with its current and new value, what is left out and how to
// undo it).
func autoTuneQuestion(f sysinfo.Facts, p sysctl.Plan) question {
	tf := api.TuneFacts(f)
	help := []string{i18n.T(i18n.CLITuneAutoHelp)}
	if facts := tui.TuneFactsText(&tf); facts != "" {
		help = append(help, i18n.T(i18n.CLITunePreviewFacts, facts))
	}
	switch {
	case f.Virt != sysinfo.VirtNone:
		help = append(help, i18n.T(i18n.CLITunePreviewInCont, f.Virt))
	case len(p.Changes) == 0:
		help = append(help, i18n.T(i18n.CLITunePreviewNone))
	default:
		later := 0
		kw, fw := 0, 0
		for _, c := range p.Changes {
			if c.Effect != api.TuneEffectNow {
				later++
			}
			kw, fw = max(kw, width(c.Key)), max(fw, width(orDash(c.From)))
		}
		help = append(help, i18n.T(i18n.CLITunePreviewCount, len(p.Changes), later))
		for _, c := range p.Changes {
			line := "  " + pad(clean(c.Key), kw) + "  " + pad(orDash(clean(c.From)), fw) + "  → " + clean(c.To)
			if c.Effect != api.TuneEffectNow {
				line += "  (" + tui.TuneEffectText(c.Effect) + ")"
			}
			help = append(help, line)
		}
		if len(p.Skips) > 0 {
			sk := p.Skips[0].API()
			help = append(help, i18n.T(i18n.CLITunePreviewSkips, len(p.Skips), sk.Key, sk.Reason))
		}
	}
	help = append(help, i18n.T(i18n.CLITunePreviewMore))
	return question{title: i18n.T(i18n.CLIAskTuneAuto), help: help, def: answerYes, yesNo: true}
}

// defaultName is the host name as a slug ("hub"/"node" when it has none).
func (g *Globals) defaultName(fallback string) string {
	h, err := g.Hostname()
	if err != nil {
		return fallback
	}
	h, _, _ = strings.Cut(strings.TrimSpace(h), ".")
	hasAlnum := strings.ContainsFunc(h, func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	})
	if s := config.Slugify(h); hasAlnum && config.ValidID(s) {
		return s
	}
	return fallback
}

// checkName accepts a display name (config: up to 64 printable characters).
func checkName(s string) error {
	if strings.TrimSpace(s) == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > config.MaxNameLen {
		return usageErr(i18n.T(i18n.CLIWantName, config.MaxNameLen))
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return usageErr(i18n.T(i18n.CLIWantName, config.MaxNameLen))
		}
	}
	return nil
}

// checkControlPort accepts a free control port outside the reserved ranges.
func (g *Globals) checkControlPort(s string) error {
	p, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || !ports.ValidPort(p) {
		return deyerr.New(deyerr.P010, deyerr.Params{"input": s})
	}
	if reserved, why := ports.Reserved(p, 0); reserved {
		return deyerr.New(deyerr.P011, deyerr.Params{"port": p, "reason": why})
	}
	if g.PortBusy(p) {
		return deyerr.New(deyerr.P012, deyerr.Params{"port": fmt.Sprintf("%d/%s", p, config.ProtoTCP)}).
			WithWhy(i18n.T(i18n.CLIPortBusyWhy, p))
	}
	return nil
}

func (g *Globals) setupHub(ctx context.Context, f setupFlags, interactive bool, st *stepper) error {
	o := setup.HubOptions{
		Root: g.Root, Runner: g.Runner, Name: strings.TrimSpace(f.name), ControlPort: f.port,
		Mirror: strings.TrimSpace(g.Getenv(EnvMirror)), SysctlProfile: config.SysctlAuto,
		ApplySysctl: f.yes, StartService: !g.NoService, SocketPath: g.Socket, Now: g.Now, Logger: g.logger(),
		LookupUser: g.LookupUser, LookupGroup: g.LookupGroup, Chown: g.Chown,
	}
	if o.Name != "" {
		if err := checkName(o.Name); err != nil {
			return err
		}
	}
	if f.port != 0 {
		if err := g.checkControlPort(strconv.Itoa(f.port)); err != nil {
			return err
		}
	}
	if interactive {
		var err error
		if o.Name == "" {
			if o.Name, err = g.askQ(st.next(question{
				title: i18n.T(i18n.CLIAskHubName), help: []string{i18n.T(i18n.CLIHubNameHelp)},
				def: g.defaultName(config.RoleHub), check: checkName,
			})); err != nil {
				return err
			}
		}
		if o.PublicIP, err = g.askPublicIP(ctx, st); err != nil {
			return err
		}
		if o.ControlPort == 0 {
			def := setup.SuggestControlPort(config.DefaultControlPort, g.PortBusy)
			ans, err := g.askQ(st.next(question{
				title: i18n.T(i18n.CLIAskControlPort), help: []string{i18n.T(i18n.CLIControlPortHelp)},
				def: strconv.Itoa(def), check: g.checkControlPort,
			}))
			if err != nil {
				return err
			}
			o.ControlPort, _ = strconv.Atoi(ans)
		}
		port := o.ControlPort
		if port == 0 {
			port = config.DefaultControlPort
		}
		facts, plan := g.tunePreview(port)
		ans, err := g.askQ(st.next(autoTuneQuestion(facts, plan)))
		if err != nil {
			return err
		}
		o.ApplySysctl = ans == answerYes
		g.printSummary(
			[2]string{i18n.T(i18n.CLISummaryRole), config.RoleHub},
			[2]string{i18n.T(i18n.CLISummaryName), o.Name},
			[2]string{i18n.T(i18n.CLISummaryPublicIP), o.PublicIP},
			[2]string{i18n.T(i18n.CLISummaryControlPort), strconv.Itoa(port)},
			[2]string{i18n.T(i18n.CLISummaryKernel), autoLabel(o.ApplySysctl, len(plan.Changes))},
		)
	} else if o.Name == "" {
		o.Name = g.defaultName(config.RoleHub)
	}
	p := g.newProgress()
	o.Progress = p.step
	if !g.JSON {
		g.say(i18n.CLISetupHubStart, o.Name)
	}
	res, err := g.Ops.SetupHub(ctx, o)
	if err != nil {
		return err
	}
	if !o.ApplySysctl && !interactive && !g.JSON {
		g.say(i18n.CLISysctlSkipped)
	}
	jc, jerr := g.joinCommand(ctx, 0)
	if g.JSON {
		doc := map[string]any{
			"role": config.RoleHub, "name": o.Name, "public_ip": res.PublicIP, "public_ip6": res.PublicIP6,
			"private_ip": res.PrivateIP, "control_port": res.ControlPort, "ca_fingerprint": res.CAFingerprint,
			"config_path": res.ConfigPath, "sysctl_profile": res.SysctlProfile, "sysctl_warnings": nonNil(res.SysctlWarnings),
			"firewall_managed": res.FirewallManaged, "service_started": res.ServiceStarted, "steps": p.steps,
		}
		if jerr == nil {
			doc["join"] = jc
		} else {
			doc["join_error"] = api.ToDTO(jerr)
		}
		return g.emitJSON(doc)
	}
	g.println()
	g.println(g.styleOut(styleGreen, g.text(g.sym().ok+" "+i18n.T(i18n.CLISetupHubDone, o.Name, res.PublicIP, res.ControlPort))))
	if res.PrivateIP {
		g.say(i18n.CLISetupPrivateIP, res.PublicIP)
	}
	for _, w := range res.SysctlWarnings {
		g.println(g.text("  " + g.sym().warn + " " + clean(w)))
	}
	g.println()
	g.println(g.styleOut(styleBold, g.text(i18n.T(i18n.CLINextTitle))))
	if jerr != nil {
		g.printf("%s", g.text(deyerr.As(jerr).Format(g.unicode())))
		g.say(i18n.CLIJoinCmdLater)
		return nil
	}
	left := jc.ExpiresAt.Sub(g.Now()).Round(time.Minute)
	g.println("   " + g.text(i18n.T(i18n.CLINextJoin, localTime(jc.ExpiresAt, "15:04"), shortDuration(left))))
	g.println()
	g.println(jc.Command)
	g.println()
	g.println("   " + g.text(i18n.T(i18n.CLINextTunnel)))
	g.println("      " + g.text(i18n.T(i18n.CLINextTunnelMenu)))
	g.println("      " + g.text(i18n.T(i18n.CLINextTunnelCLI)))
	return nil
}

// askPublicIP asks for the public IP with the detected one as default
// (question 3 of the hub wizard); a private detection is explained.
func (g *Globals) askPublicIP(ctx context.Context, st *stepper) (string, error) {
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	ip, private, err := g.DetectIP(dctx)
	cancel()
	switch {
	case err != nil:
		fmt.Fprint(g.promptOut(), g.text(deyerr.As(err).Format(g.unicode())))
		ip = ""
	case private:
		g.note(i18n.CLIDetectedPrivate, ip)
		ip = ""
	}
	help := []string{i18n.T(i18n.CLIPublicIPHelp)}
	if ip != "" {
		help = append(help, i18n.T(i18n.CLIPublicIPDetected, ip))
	}
	return g.askQ(st.next(question{
		title: i18n.T(i18n.CLIAskPublicIP), help: help, def: ip,
		check: func(s string) error {
			a, err := netip.ParseAddr(strings.TrimSpace(s))
			if err != nil {
				return deyerr.New(deyerr.C013, deyerr.Params{"field": "hub.public_ip", "value": s, "allowed": i18n.T(i18n.CLIWantIP)})
			}
			if !setup.IsPublicIP(a) {
				g.note(i18n.CLIDetectedPrivate, a.String())
			}
			return nil
		},
	}))
}

// printSummary prints the answers of a wizard as an aligned table before
// the work starts.
func (g *Globals) printSummary(rows ...[2]string) {
	w := g.promptOut()
	fmt.Fprintln(w)
	fmt.Fprintln(w, g.style(styleBold, g.text(i18n.T(i18n.CLISummaryTitle))))
	kw := 0
	for _, r := range rows {
		kw = max(kw, width(r[0]))
	}
	for _, r := range rows {
		fmt.Fprintln(w, "   "+g.text(pad(r[0], kw+3)+clean(r[1])))
	}
	fmt.Fprintln(w)
}

// kernelLabel describes the kernel profile answer in the summary.
func kernelLabel(apply bool) string {
	if apply {
		return i18n.T(i18n.CLIKernelBalanced)
	}
	return i18n.T(i18n.CLIKernelUnchanged)
}

// autoLabel describes the automatic tuning answer in the summary.
func autoLabel(apply bool, changes int) string {
	if apply {
		return i18n.T(i18n.CLIKernelAuto, changes)
	}
	return i18n.T(i18n.CLIKernelUnchanged)
}

// joinCommand asks the new daemon for a join command (ttl 0 = default).
func (g *Globals) joinCommand(ctx context.Context, ttl time.Duration) (api.JoinCommand, error) {
	l, err := g.local()
	if err != nil {
		return api.JoinCommand{}, err
	}
	cctx, cancel := callCtx(ctx)
	defer cancel()
	return l.NodeJoinCommand(cctx, ttl)
}

// printJoinCommand prints the one-line join command on its own line with
// its expiry (spec section 3).
func (g *Globals) printJoinCommand(jc api.JoinCommand) {
	left := jc.ExpiresAt.Sub(g.Now()).Round(time.Minute)
	g.say(i18n.CLIJoinCmdIntro, localTime(jc.ExpiresAt, "15:04"), shortDuration(left))
	g.println()
	g.println(jc.Command)
	g.println()
}

func (g *Globals) setupNode(ctx context.Context, f setupFlags, interactive bool, st *stepper) error {
	if !interactive {
		return usageErr(i18n.T(i18n.CLISetupNodeNeedsLink))
	}
	link, err := g.askQ(st.next(question{
		title: i18n.T(i18n.CLIAskJoinLink), help: []string{i18n.T(i18n.CLIJoinLinkHelp)},
		check: func(s string) error {
			_, err := api.ParseJoinLink(s)
			return err
		},
	}))
	if err != nil {
		return err
	}
	name := strings.TrimSpace(f.name)
	if name == "" {
		if name, err = g.askQ(st.next(question{
			title: i18n.T(i18n.CLIAskNodeName), help: []string{i18n.T(i18n.CLINodeNameHelp)},
			def: g.defaultName(config.RoleNode), check: checkName,
		})); err != nil {
			return err
		}
	}
	ans, err := g.askQ(st.next(kernelQuestion()))
	if err != nil {
		return err
	}
	// Only the hub address: the link carries the join token.
	hub := ""
	if jl, err := api.ParseJoinLink(link); err == nil {
		hub = jl.Addr()
	}
	g.printSummary(
		[2]string{i18n.T(i18n.CLISummaryRole), config.RoleNode},
		[2]string{i18n.T(i18n.CLISummaryName), name},
		[2]string{i18n.T(i18n.CLISummaryHub), hub},
		[2]string{i18n.T(i18n.CLISummaryKernel), kernelLabel(ans == answerYes)},
	)
	return g.join(ctx, link, name, ans == answerYes)
}

// join joins this server to a hub (setup.Join) and prints the result.
func (g *Globals) join(ctx context.Context, link, name string, applySysctl bool) error {
	if name != "" {
		if err := checkName(name); err != nil {
			return err
		}
	}
	p := g.newProgress()
	res, err := g.Ops.Join(ctx, setup.JoinOptions{
		Root: g.Root, Runner: g.Runner, Link: link, Name: name, ApplySysctl: applySysctl,
		SysctlProfile: config.SysctlBalanced, StartService: !g.NoService, SocketPath: g.Socket,
		Now: g.Now, Progress: p.step, Logger: g.logger(), Hostname: g.Hostname,
		LookupUser: g.LookupUser, LookupGroup: g.LookupGroup, Chown: g.Chown,
	})
	if err != nil {
		return err
	}
	hubName := res.HubName
	if hubName == "" {
		hubName = res.HubAddr
	}
	if res.Front {
		// The hub is shown as DOMAIN:PORT; the path secret is never printed.
		hubName += " (" + i18n.T(i18n.CLIViaFront) + ")"
	}
	if g.JSON {
		return g.emitJSON(map[string]any{
			"role": config.RoleNode, "node": res.NodeID, "hub_name": res.HubName, "hub_addr": res.HubAddr,
			"hub_version": res.HubVersion, "public_ip": res.PublicIP, "ca_fingerprint": res.CAFingerprint,
			"front": res.Front, "compatible": res.Compatible, "config_path": res.ConfigPath, "sysctl_profile": res.SysctlProfile,
			"sysctl_warnings": nonNil(res.SysctlWarnings), "service_started": res.ServiceStarted, "steps": p.steps,
		})
	}
	for _, w := range res.SysctlWarnings {
		g.println(g.text("  " + g.sym().warn + " " + clean(w)))
	}
	g.println()
	g.println(g.styleOut(styleGreen, g.text(g.sym().ok+" "+i18n.T(i18n.CLIJoinDone, res.NodeID, hubName))))
	if !res.Compatible && res.HubVersion != "" {
		g.say(i18n.CLIJoinIncompatible, res.HubVersion, version.Version)
	}
	g.println()
	g.println(g.styleOut(styleBold, g.text(i18n.T(i18n.CLINodeNextTitle, hubName))))
	g.println("   " + g.text(i18n.T(i18n.CLINodeNextHub, res.NodeID)))
	return nil
}

// shortDuration renders whole minutes compactly: "15m", "1h", "1h30m".
func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return "0m"
	}
	s := strings.TrimSuffix(d.Round(time.Minute).String(), "0s")
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// exactArgs is cobra.ExactArgs with the error as a usage error.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usageErr(i18n.T(i18n.CLIWantArgs, cmd.CommandPath(), n, len(args)))
		}
		return nil
	}
}

// rangeArgs is cobra.RangeArgs with the error as a usage error.
func rangeArgs(lo, hi int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < lo || len(args) > hi {
			return usageErr(i18n.T(i18n.CLIWantArgsRange, cmd.CommandPath(), lo, hi, len(args)))
		}
		return nil
	}
}
