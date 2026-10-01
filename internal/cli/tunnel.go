package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/ports"
	"github.com/localroot4/deyroute/internal/state"
)

// testLadderRung is how long test-ladder tries each rung (spec section 9).
const testLadderRung = 20 * time.Second

func newTunnelCmd(g *Globals) *cobra.Command {
	cmd := newGroup("tunnel", i18n.CLITunnelShort)
	backup := newGroup("backup", i18n.CLITunnelBackupShort)
	backup.AddCommand(newTunnelBackupCmd(g, true), newTunnelBackupCmd(g, false))
	cmd.AddCommand(newTunnelAddCmd(g), newTunnelListCmd(g), newTunnelShowCmd(g), newTunnelEditCmd(g),
		newTunnelEnableCmd(g, true), newTunnelEnableCmd(g, false), newTunnelRestartCmd(g), newTunnelDeleteCmd(g),
		newTunnelSwitchCmd(g), newTunnelSimpleCmd(g, "reset"), newTunnelSimpleCmd(g, "pause"), newTunnelSimpleCmd(g, "resume"),
		newTunnelTestLadderCmd(g), backup)
	return cmd
}

// ladderArg splits a --ladder value: a profile name ("default") or an
// inline, comma-separated list of transports ("backhaul/wssmux,rathole/noise").
func ladderArg(s string) (profile string, rungs []string) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "/") {
		return s, nil
	}
	return "", splitList(s)
}

// splitList splits a comma-separated list, dropping empty items.
func splitList(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// portSpecs converts parsed port input into Local API specs.
func portSpecs(in []ports.Spec) []api.PortSpec {
	out := make([]api.PortSpec, 0, len(in))
	for _, s := range in {
		out = append(out, api.PortSpec{Listen: s.Listen, Proto: s.Proto, Target: s.Target})
	}
	return out
}

func newTunnelAddCmd(g *Globals) *cobra.Command {
	var node, portsIn, name, ladder, tlsMode string
	var backups []string
	var yes bool
	cmd := &cobra.Command{
		Use:     "add --node <id> --ports 443,2053[,27015/udp]",
		Short:   i18n.T(i18n.CLITunnelAddShort),
		Long:    i18n.T(i18n.CLITunnelAddLong),
		Example: i18n.T(i18n.CLITunnelAddExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(node) == "" {
				return usageErr(i18n.T(i18n.CLIWantFlag, "--node"))
			}
			if strings.TrimSpace(portsIn) == "" {
				return usageErr(i18n.T(i18n.CLIWantFlag, "--ports"))
			}
			specs, err := ports.ParseInput(portsIn)
			if err != nil {
				return err
			}
			req := api.TunnelAddRequest{Name: strings.TrimSpace(name), Node: strings.TrimSpace(node), Ports: portSpecs(specs)}
			if cmd.Flags().Changed("tls-mode") {
				// custom needs certificate files, which TunnelAdd does not
				// take: it is set afterwards with tunnel edit.
				if req.TLSMode, err = tlsModeArg(tlsMode, config.TLSModeAuto, config.TLSModeACME); err != nil {
					return err
				}
			}
			for _, b := range backups {
				req.Backups = append(req.Backups, splitList(b)...)
			}
			req.Ladder, req.Rungs = ladderArg(ladder)
			l, err := g.local()
			if err != nil {
				return err
			}
			if g.IsTTY && !yes {
				ladderText := ladder
				if ladderText == "" {
					ladderText = i18n.T(i18n.CLILadderDefault)
				}
				g.note(i18n.CLITunnelAddSummary, req.Node, ports.FormatList(specs), ladderText, orDash(strings.Join(req.Backups, ", ")))
				if len(req.Backups) > 0 {
					g.note(i18n.TUIBkWarning)
				}
				okd, err := g.askYesNo(i18n.T(i18n.CLIAskCreateTunnel), true)
				if err != nil {
					return err
				}
				if !okd {
					return errAborted
				}
			}
			p := g.newProgress()
			ctx, cancel := longCtx(cmd.Context())
			defer cancel()
			t, err := l.TunnelAdd(ctx, req, p.step)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"tunnel": t, "steps": p.steps})
			}
			g.printTunnelResult(t)
			if len(req.Backups) > 0 {
				g.say(i18n.TUIBkWarning)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&node, "node", "", i18n.T(i18n.CLIFlagTunnelNode))
	f.StringVar(&portsIn, "ports", "", i18n.T(i18n.CLIFlagPorts))
	f.StringVar(&name, "name", "", i18n.T(i18n.CLIFlagTunnelName))
	f.StringVar(&ladder, "ladder", "", i18n.T(i18n.CLIFlagLadder))
	f.StringArrayVar(&backups, "backup", nil, i18n.T(i18n.CLIFlagBackupNode))
	f.StringVar(&tlsMode, "tls-mode", "", i18n.T(i18n.CLIFlagTLSModeAdd))
	f.BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYesAdd))
	return cmd
}

// tlsModeArg checks a --tls-mode value against the allowed modes.
func tlsModeArg(v string, allowed ...string) (string, error) {
	m := strings.ToLower(strings.TrimSpace(v))
	if !slices.Contains(allowed, m) {
		return "", deyerr.New(deyerr.C013, deyerr.Params{"field": "--tls-mode", "value": v, "allowed": strings.Join(allowed, ", ")})
	}
	return m, nil
}

// printTunnelResult prints "Tunnel main is UP via backhaul/wssmux (41ms)"
// or the state the tunnel is in.
func (g *Globals) printTunnelResult(t api.TunnelInfo) {
	if t.State == state.StateUp && t.ActiveTransport != "" {
		g.say(i18n.TUITunnelUp, t.ID, t.ActiveTransport, t.RTTms)
		return
	}
	g.say(i18n.CLITunnelState, t.ID, g.stateCell(t))
}

func newTunnelListCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   i18n.T(i18n.CLITunnelListShort),
		Example: i18n.T(i18n.CLITunnelListExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var ts []api.TunnelInfo
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				ts, err = l.TunnelList(ctx)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"tunnels": nonNil(ts)})
			}
			if len(ts) == 0 {
				g.say(i18n.CLIStatusNoTunnels)
				return nil
			}
			rows := make([][]string, 0, len(ts))
			for _, t := range ts {
				rtt := orDash("")
				if t.State == state.StateUp || t.State == state.StateDegraded {
					rtt = ms(t.RTTms)
				}
				rows = append(rows, []string{t.ID, orDash(t.Name), g.stateCell(t), orDash(t.ActiveNode), orDash(t.ActiveTransport),
					rtt, portsText(t.Ports), strings.Join(t.Nodes, ",")})
			}
			g.table([]string{i18n.T(i18n.CLIColID), i18n.T(i18n.TUIColName), i18n.T(i18n.TUIColState), i18n.T(i18n.CLIColNode),
				i18n.T(i18n.TUIColTransport), i18n.T(i18n.TUIColRTT), i18n.T(i18n.TUIColPorts), i18n.T(i18n.CLIColNodes)}, rows)
			return nil
		},
	}
}

func newTunnelShowCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:     "show <id>",
		Short:   i18n.T(i18n.CLITunnelShowShort),
		Example: i18n.T(i18n.CLITunnelShowExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var d api.TunnelDetail
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				d, err = l.TunnelShow(ctx, args[0])
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(d)
			}
			g.printTunnelDetail(d)
			return nil
		},
	}
}

// printTunnelDetail prints `deyroute tunnel show`: ports, ladder, active
// transport, rungs, probe history, metrics and recent events.
func (g *Globals) printTunnelDetail(d api.TunnelDetail) {
	s := g.sym()
	name := d.Name
	if name == "" {
		name = d.ID
	}
	kv := func(k i18n.Key, v string) { g.println(g.text("  " + pad(i18n.T(k), 13) + v)) }
	g.println(g.text(fmt.Sprintf("%s (%s)  %s", d.ID, clean(name), g.stateCell(d.TunnelInfo))))
	var nodes []string
	for i, n := range d.Nodes {
		role := i18n.T(i18n.CLIBackupRole)
		if i == 0 {
			role = i18n.T(i18n.CLIPrimaryRole)
		}
		nodes = append(nodes, n+" ("+role+")")
	}
	kv(i18n.CLIKeyNodes, orDash(strings.Join(nodes, ", ")))
	active := orDash("")
	if d.ActiveTransport != "" {
		active = i18n.T(i18n.CLIActiveVia, orDash(d.ActiveNode), d.ActiveTransport)
		if d.RTTms > 0 {
			active += ", " + ms(d.RTTms)
		}
		if !d.UpSince.IsZero() && (d.State == state.StateUp || d.State == state.StateDegraded) {
			active += ", " + i18n.T(i18n.CLIUpFor, upTime(g.Now().Sub(d.UpSince)))
		}
	}
	kv(i18n.CLIKeyActive, active)
	var ps []string
	for _, p := range d.Ports {
		x := fmt.Sprintf("%d/%s %s %s", p.Listen, p.Proto, s.arrow, p.Target)
		if p.Probe != "" {
			x += " (" + p.Probe + ")"
		}
		ps = append(ps, x)
	}
	kv(i18n.CLIKeyPorts, orDash(strings.Join(ps, ", ")))
	lad := strings.Join(d.Ladder, ", ")
	if d.LadderName != "" {
		lad = d.LadderName + ": " + lad
	}
	kv(i18n.CLIKeyLadder, orDash(lad))
	kv(i18n.CLIKeyPolicy, i18n.T(i18n.CLIPolicyLine, orDash(d.Failover.Policy), yesNo(d.Paused)))
	probePort := orDash("")
	if d.ProbePort > 0 {
		probePort = strconv.Itoa(d.ProbePort)
	}
	kv(i18n.CLIKeyProbe, i18n.T(i18n.CLIProbeLine, probePort, orDash(d.TLSMode), orDash(d.ClientIP)))
	f := d.Failover
	failback := yesNo(f.Failback)
	if f.Failback {
		failback = i18n.T(i18n.CLIFailbackAfter, f.FailbackAfterS)
		if d.FailbackDelay > 0 {
			failback = i18n.T(i18n.CLIFailbackAfter, int(d.FailbackDelay/time.Second))
		}
	}
	kv(i18n.CLIKeyFailover, i18n.T(i18n.CLIFailoverLine, f.ProbeIntervalS, f.ProbeTimeoutS, f.FailThreshold,
		f.RecoverThreshold, failback, f.MaxSwitchesPerHour, f.QuarantineS))
	if d.Metrics != nil {
		kv(i18n.CLIKeyTraffic, i18n.T(i18n.CLITrafficLine, humanBytes(d.Metrics.BytesIn), humanBytes(d.Metrics.BytesOut), d.Metrics.ActiveConns))
	}
	for _, w := range d.Warnings {
		g.println(g.text("  " + s.warn + " " + clean(w)))
	}
	if len(d.Rungs) > 0 {
		g.println(g.text(" " + i18n.T(i18n.CLIRungsTitle)))
		rows := make([][]string, 0, len(d.Rungs))
		for _, r := range d.Rungs {
			st := i18n.T(i18n.CLIRungWarm)
			switch {
			case r.Active:
				st = s.up + " " + i18n.T(i18n.CLIRungActive)
			case r.Skipped != "":
				st = s.warn + " " + i18n.T(i18n.CLIRungSkipped, clean(r.Skipped))
			case !r.Quarantine.IsZero() && r.Quarantine.After(g.Now()):
				st = i18n.T(i18n.CLIRungQuarantine, localTime(r.Quarantine, "15:04:05"))
			case !r.Warm:
				st = i18n.T(i18n.CLIRungCold)
			}
			ctl := orDash("")
			if r.ControlPort > 0 {
				ctl = strconv.Itoa(r.ControlPort)
			}
			rows = append(rows, []string{r.Node, r.Transport, st, ctl, orDash(r.UnitState)})
		}
		g.table([]string{i18n.T(i18n.CLIColNode), i18n.T(i18n.TUIColTransport), i18n.T(i18n.TUIColState),
			i18n.T(i18n.CLIColCtlPort), i18n.T(i18n.CLIColUnit)}, rows)
	}
	g.println(g.text(" " + i18n.T(i18n.CLIProbesTitle)))
	g.println(g.text("  " + g.probeSummary(d.Probes)))
	if len(d.Events) > 0 {
		g.println(g.text(" " + i18n.T(i18n.TUIDashEvents)))
		g.printf("%s", g.text(g.eventLines(d.Events)))
	}
}

// probeSummary condenses the probe history: count, failures, median RTT
// and a strip of the last 40 results ("✔✔✖✔").
func (g *Globals) probeSummary(ps []state.ProbeSample) string {
	if len(ps) == 0 {
		return i18n.T(i18n.CLIProbesNone)
	}
	okCount := 0
	var rtts []time.Duration
	var lastFail *state.ProbeSample
	for i := range ps {
		if ps[i].OK {
			okCount++
			rtts = append(rtts, ps[i].RTT)
		} else {
			lastFail = &ps[i]
		}
	}
	med := 0
	if len(rtts) > 0 {
		sortDurations(rtts)
		med = int(rtts[len(rtts)/2] / time.Millisecond)
	}
	line := i18n.T(i18n.CLIProbesLine, len(ps), okCount, len(ps)-okCount, ms(med))
	if lastFail != nil {
		line += "; " + i18n.T(i18n.CLIProbesLastFail, localTime(lastFail.At, "15:04:05"), orDash(clean(lastFail.Error)))
	}
	start := max(0, len(ps)-40)
	var strip strings.Builder
	okMark, failMark := "✔", "✖"
	if !g.unicode() {
		okMark, failMark = "+", "x"
	}
	for _, p := range ps[start:] {
		if p.OK {
			strip.WriteString(okMark)
		} else {
			strip.WriteString(failMark)
		}
	}
	return line + "\n  " + strip.String()
}

func sortDurations(d []time.Duration) { slices.Sort(d) }

// humanBytes formats a byte count ("1.5 GB").
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}

func newTunnelEditCmd(g *Globals) *cobra.Command {
	var name, ladder, policy, tlsMode, tlsCert, tlsKey string
	var probePort int
	cmd := &cobra.Command{
		Use:     "edit <id>",
		Short:   i18n.T(i18n.CLITunnelEditShort),
		Example: i18n.T(i18n.CLITunnelEditExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req api.TunnelEditRequest
			fl := cmd.Flags()
			changed := false
			if fl.Changed("name") {
				if err := checkName(name); err != nil {
					return err
				}
				n := strings.TrimSpace(name)
				req.Name, changed = &n, true
			}
			if fl.Changed("ladder") {
				profile, rungs := ladderArg(ladder)
				if rungs != nil {
					req.Rungs = rungs
				} else {
					req.Ladder = &profile
				}
				changed = true
			}
			if fl.Changed("policy") {
				p := strings.TrimSpace(policy)
				req.Policy, changed = &p, true
			}
			if fl.Changed("probe-port") {
				pp := probePort
				req.ProbePort, changed = &pp, true
			}
			if fl.Changed("tls-mode") {
				m, err := tlsModeArg(tlsMode, config.TLSModes...)
				if err != nil {
					return err
				}
				req.TLSMode, changed = &m, true
			}
			for _, f := range []struct {
				flag string
				val  string
				dst  **string
			}{{"tls-cert", tlsCert, &req.TLSCert}, {"tls-key", tlsKey, &req.TLSKey}} {
				if !fl.Changed(f.flag) {
					continue
				}
				// The daemon reads the files with its own working
				// directory (/): a relative path is resolved here.
				abs, err := filepath.Abs(strings.TrimSpace(f.val))
				if err != nil || strings.TrimSpace(f.val) == "" {
					return deyerr.New(deyerr.C013, deyerr.Params{"field": "--" + f.flag, "value": f.val, "allowed": i18n.T(i18n.CLIWantAbsPathPEM)})
				}
				*f.dst, changed = &abs, true
			}
			if !changed {
				return usageErr(i18n.T(i18n.CLIEditNothing))
			}
			p := g.newProgress()
			var t api.TunnelInfo
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				t, err = l.TunnelEdit(ctx, args[0], req, p.step)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"tunnel": t, "steps": p.steps})
			}
			g.say(i18n.CLITunnelUpdated, args[0])
			g.printTunnelResult(t)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", i18n.T(i18n.CLIFlagTunnelName))
	f.StringVar(&ladder, "ladder", "", i18n.T(i18n.CLIFlagLadder))
	f.StringVar(&policy, "policy", "", i18n.T(i18n.CLIFlagPolicy))
	f.IntVar(&probePort, "probe-port", 0, i18n.T(i18n.CLIFlagProbePort))
	f.StringVar(&tlsMode, "tls-mode", "", i18n.T(i18n.CLIFlagTLSMode))
	f.StringVar(&tlsCert, "tls-cert", "", i18n.T(i18n.CLIFlagTLSCert))
	f.StringVar(&tlsKey, "tls-key", "", i18n.T(i18n.CLIFlagTLSKey))
	return cmd
}

func newTunnelEnableCmd(g *Globals, enable bool) *cobra.Command {
	use, short, example := "enable <id>", i18n.CLITunnelEnableShort, i18n.CLITunnelEnableExample
	if !enable {
		use, short, example = "disable <id>", i18n.CLITunnelDisableShort, i18n.CLITunnelDisableExample
	}
	var yes bool
	cmd := &cobra.Command{
		Use:     use,
		Short:   i18n.T(short),
		Example: i18n.T(example),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			l, err := g.local()
			if err != nil {
				return err
			}
			if !enable {
				if err := g.confirm(i18n.T(i18n.CLITunnelDisableLost, id), yes); err != nil {
					return err
				}
			}
			ctx, cancel := longCtx(cmd.Context())
			defer cancel()
			if err := l.TunnelSetEnabled(ctx, id, enable); err != nil {
				return err
			}
			k := i18n.CLITunnelEnabled
			if !enable {
				k = i18n.CLITunnelDisabled
			}
			return g.done(map[string]any{"tunnel": id, "enabled": enable}, k, id)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

func newTunnelRestartCmd(g *Globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "restart <id>",
		Short:   i18n.T(i18n.CLITunnelRestartShort),
		Example: i18n.T(i18n.CLITunnelRestartExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !g.JSON && !yes {
				g.say(i18n.CLITunnelRestartNote, id)
			}
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) error {
				return l.TunnelRestart(ctx, id)
			})
			if err != nil {
				return err
			}
			return g.done(map[string]any{"tunnel": id}, i18n.CLITunnelRestarted, id)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

func newTunnelDeleteCmd(g *Globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Short:   i18n.T(i18n.CLITunnelDeleteShort),
		Example: i18n.T(i18n.CLITunnelDeleteExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			l, err := g.local()
			if err != nil {
				return err
			}
			if err := g.confirm(i18n.T(i18n.CLITunnelDeleteLost, id), yes); err != nil {
				return err
			}
			p := g.newProgress()
			ctx, cancel := longCtx(cmd.Context())
			defer cancel()
			if err := l.TunnelDelete(ctx, id, p.step); err != nil {
				return err
			}
			if g.JSON {
				return g.ok(map[string]any{"tunnel": id}, p.steps)
			}
			g.say(i18n.CLITunnelDeleted, id)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

func newTunnelSwitchCmd(g *Globals) *cobra.Command {
	var transport, node string
	cmd := &cobra.Command{
		Use:     "switch <id> --transport <id> | --node <id>",
		Short:   i18n.T(i18n.CLITunnelSwitchShort),
		Example: i18n.T(i18n.CLITunnelSwitchExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			transport, node = strings.TrimSpace(transport), strings.TrimSpace(node)
			if (transport == "") == (node == "") {
				return usageErr(i18n.T(i18n.CLISwitchOneOf))
			}
			req := api.SwitchRequest{Transport: transport, Node: node}
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) error {
				return l.TunnelSwitch(ctx, args[0], req)
			})
			if err != nil {
				return err
			}
			target := transport
			if node != "" {
				target = node
			}
			return g.done(map[string]any{"tunnel": args[0], "transport": transport, "node": node}, i18n.CLITunnelSwitched, args[0], target)
		},
	}
	cmd.Flags().StringVar(&transport, "transport", "", i18n.T(i18n.CLIFlagSwitchTransport))
	cmd.Flags().StringVar(&node, "node", "", i18n.T(i18n.CLIFlagSwitchNode))
	return cmd
}

// newTunnelSimpleCmd builds reset, pause and resume: one id, one call.
func newTunnelSimpleCmd(g *Globals, verb string) *cobra.Command {
	var short, example, doneKey i18n.Key
	var fn func(ctx context.Context, l api.Local, id string) error
	switch verb {
	case "reset":
		short, example, doneKey = i18n.CLITunnelResetShort, i18n.CLITunnelResetExample, i18n.CLITunnelResetDone
		fn = func(ctx context.Context, l api.Local, id string) error { return l.TunnelReset(ctx, id) }
	case "pause":
		short, example, doneKey = i18n.CLITunnelPauseShort, i18n.CLITunnelPauseExample, i18n.CLITunnelPaused
		fn = func(ctx context.Context, l api.Local, id string) error { return l.TunnelPause(ctx, id) }
	default:
		short, example, doneKey = i18n.CLITunnelResumeShort, i18n.CLITunnelResumeExample, i18n.CLITunnelResumed
		fn = func(ctx context.Context, l api.Local, id string) error { return l.TunnelResume(ctx, id) }
	}
	return &cobra.Command{
		Use:     verb + " <id>",
		Short:   i18n.T(short),
		Example: i18n.T(example),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) error { return fn(ctx, l, args[0]) })
			if err != nil {
				return err
			}
			return g.done(map[string]any{"tunnel": args[0]}, doneKey, args[0])
		},
	}
}

func newTunnelTestLadderCmd(g *Globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "test-ladder <id>",
		Short:   i18n.T(i18n.CLITunnelTestLadderShort),
		Example: i18n.T(i18n.CLITunnelTestLadderExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			l, err := g.local()
			if err != nil {
				return err
			}
			if err := g.confirm(i18n.T(i18n.CLITestLadderLost, id, int(testLadderRung/time.Second)), yes); err != nil {
				return err
			}
			p := g.newProgress()
			ctx, cancel := longCtx(cmd.Context())
			defer cancel()
			res, err := l.TunnelTestLadder(ctx, id, p.step)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"tunnel": id, "results": nonNil(res), "steps": p.steps})
			}
			s := g.sym()
			rows := make([][]string, 0, len(res))
			for _, r := range res {
				result, detail := s.ok+" "+i18n.T(i18n.CLIResultOK), ""
				switch {
				case r.Skipped != "":
					result, detail = s.skip+" "+i18n.T(i18n.CLIResultSkipped), clean(r.Skipped)
				case !r.OK:
					result = s.fail + " " + i18n.T(i18n.CLIResultFailed)
					if r.Error != nil {
						detail = r.Error.Code + " " + clean(r.Error.Message)
					}
				}
				rtt := orDash("")
				if r.OK {
					rtt = ms(r.RTTms)
				}
				rows = append(rows, []string{r.Node, r.Transport, result, rtt, detail})
			}
			g.table([]string{i18n.T(i18n.CLIColNode), i18n.T(i18n.TUIColTransport), i18n.T(i18n.CLIColResult),
				i18n.T(i18n.TUIColRTT), i18n.T(i18n.CLIColDetail)}, rows)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

func newTunnelBackupCmd(g *Globals, add bool) *cobra.Command {
	var nodes []string
	var yes bool
	use, short, example := "add <id> --node <nid>", i18n.CLITunnelBackupAddShort, i18n.CLITunnelBackupAddExample
	if !add {
		use, short, example = "remove <id> --node <nid>", i18n.CLITunnelBackupRemoveShort, i18n.CLITunnelBackupRemoveExample
	}
	cmd := &cobra.Command{
		Use:     use,
		Short:   i18n.T(short),
		Example: i18n.T(example),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(nodes) == 0 || strings.TrimSpace(nodes[0]) == "" {
				return usageErr(i18n.T(i18n.CLIWantFlag, "--node"))
			}
			id, nid := args[0], strings.TrimSpace(nodes[0])
			if len(nodes) > 1 || strings.Contains(nid, ",") {
				return usageErr(i18n.T(i18n.CLIOneBackupNode))
			}
			if !add {
				if err := g.confirm(i18n.T(i18n.TUIBkRemoveLost, nid, id, id, nid), yes); err != nil {
					return err
				}
			}
			p := g.newProgress()
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) error {
				if add {
					return l.TunnelBackupAdd(ctx, id, nid, p.step)
				}
				return l.TunnelBackupRemove(ctx, id, nid)
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.ok(map[string]any{"tunnel": id, "node": nid}, p.steps)
			}
			if add {
				g.say(i18n.TUIBkReady, nid)
				g.say(i18n.TUIBkWarning)
				return nil
			}
			g.say(i18n.CLIBackupRemoved, nid, id)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&nodes, "node", nil, i18n.T(i18n.CLIFlagBackupNodeOne))
	if !add {
		cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	}
	return cmd
}
