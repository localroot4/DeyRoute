package tui

import (
	"context"
	"net"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/ports"
)

// wizStep is the current question of the Add tunnel wizard.
type wizStep int

const (
	wzNode     wizStep = iota // 1. Which node?
	wzPorts                   // 2. Ports?
	wzChecking                // PortCheck running
	wzResolve                 // a busy port: change / skip / stop
	wzNewPort                 // typing the replacement port
	wzConfirm                 // 3. Confirm
)

// portCheck is one entered port and its PortCheck answer.
type portCheck struct {
	spec ports.Spec
	res  *api.PortCheckResult
	err  error
}

func (p portCheck) ok() bool { return p.err == nil && p.res != nil && p.res.BindFree }

// wizChecked is the result of checking the ports at idx.
type wizChecked struct {
	idx []int
	res []portCheck
}

// wizard is the Add tunnel wizard of section 6: in Simple mode at most three
// questions (node, ports, confirm); Advanced adds the ladder editor and an
// options form (targets, backup node, policy, TLS mode, thresholds).
type wizard struct {
	screenBase
	step     wizStep
	loading  bool
	loadErr  error
	nodes    []api.NodeInfo // online nodes
	node     api.NodeInfo
	auto     bool
	input    string
	inputErr error
	ch       chooser
	checks   []portCheck
	cur      int
	msg      string

	// Advanced options.
	advDone  bool
	name     string
	rungs    []string
	backup   string
	policy   string
	tls      string
	failover *api.FailoverSettings
}

func newWizard() *wizard {
	return &wizard{screenBase: screenBase{title: i18n.T(i18n.TUITunAdd), help: i18n.TUIHelpWizard}}
}

func (w *wizard) start(a *app) tea.Cmd {
	w.loading = true
	return a.call(w, callTimeout, loadNodes)
}

func (w *wizard) setStep(s wizStep) {
	w.step = s
	w.typing = s == wzPorts || s == wzNewPort
}

// back goes one question back; on the first question it leaves the wizard.
func (w *wizard) back(*app) bool {
	switch w.step {
	case wzNewPort:
		w.setStep(wzResolve)
	case wzResolve, wzConfirm:
		w.setStep(wzPorts)
	case wzPorts:
		if w.auto || len(w.nodes) < 2 {
			return false
		}
		w.setStep(wzNode)
	default:
		return false
	}
	w.inputErr, w.msg = nil, ""
	return true
}

func (w *wizard) update(a *app, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case asyncMsg:
		if d, ok := msg.payload.(donePayload); ok {
			return w.done(a, d)
		}
	case tea.KeyMsg:
		return w.keyMsg(a, msg)
	}
	return nil
}

func (w *wizard) done(a *app, d donePayload) tea.Cmd {
	switch w.step {
	case wzNode:
		w.loading = false
		if d.err != nil {
			w.loadErr = d.err
			return nil
		}
		all, _ := d.v.([]api.NodeInfo)
		w.nodes = nil
		for _, n := range all {
			if n.Online {
				w.nodes = append(w.nodes, n)
			}
		}
		if len(w.nodes) == 1 {
			w.node, w.auto = w.nodes[0], true
			w.setStep(wzPorts)
		}
		return nil
	case wzChecking:
		if d.err != nil {
			w.inputErr = d.err
			w.setStep(wzResolve)
			return nil
		}
		r, _ := d.v.(wizChecked)
		for i, idx := range r.idx {
			if idx < len(w.checks) {
				w.checks[idx] = r.res[i]
			}
		}
		return w.afterCheck(a)
	}
	return nil
}

// afterCheck moves to the first problem port, or on to the confirmation.
func (w *wizard) afterCheck(a *app) tea.Cmd {
	for i, c := range w.checks {
		if !c.ok() {
			w.cur = i
			w.setStep(wzResolve)
			return nil
		}
	}
	w.setStep(wzConfirm)
	if a.advanced && !w.advDone {
		w.advDone = true
		return w.openAdvanced(a)
	}
	return nil
}

func (w *wizard) keyMsg(a *app, k tea.KeyMsg) tea.Cmd {
	switch w.step {
	case wzNode:
		return w.keyNode(a, k)
	case wzPorts:
		if k.Type != tea.KeyEnter {
			editLine(&w.input, k)
			return nil
		}
		return w.submitPorts(a)
	case wzResolve:
		return w.keyResolve(a, k)
	case wzNewPort:
		if k.Type != tea.KeyEnter {
			editLine(&w.input, k)
			return nil
		}
		return w.submitNewPort(a)
	case wzConfirm:
		return w.keyConfirm(a, k)
	}
	return nil
}

func (w *wizard) keyNode(a *app, k tea.KeyMsg) tea.Cmd {
	if k.String() == "r" && !w.loading {
		w.loadErr = nil
		return w.start(a)
	}
	if submit, _ := w.ch.key(k); !submit {
		return nil
	}
	n, raw, ok := w.ch.take()
	w.msg = ""
	switch {
	case ok && n == 0:
		return a.pop()
	case !ok || n < 1 || n > len(w.nodes):
		if raw != "" {
			w.msg = i18n.T(i18n.InvalidChoice, raw)
		}
		return nil
	}
	w.node = w.nodes[n-1]
	w.setStep(wzPorts)
	return nil
}

func (w *wizard) submitPorts(a *app) tea.Cmd {
	w.msg = ""
	specs, err := ports.ParseInput(w.input)
	if err == nil && len(specs) == 0 {
		err = uiErr(i18n.TUIRequired)
	}
	if err != nil {
		w.inputErr = err
		return nil
	}
	w.inputErr = nil
	w.checks = make([]portCheck, len(specs))
	idx := make([]int, len(specs))
	for i, s := range specs {
		w.checks[i] = portCheck{spec: s}
		idx[i] = i
	}
	return w.check(a, idx)
}

// check runs PortCheck for the ports at idx (one Local call per port).
func (w *wizard) check(a *app, idx []int) tea.Cmd {
	w.setStep(wzChecking)
	node := w.node.ID
	specs := make([]ports.Spec, len(idx))
	for i, x := range idx {
		specs[i] = w.checks[x].spec
	}
	// No overall timeout: a range of up to 64 ports is checked one by one,
	// each call with its own timeout (runChecks).
	return a.call(w, 0, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		return runChecks(ctx, l, node, idx, specs), nil
	})
}

// runChecks runs PortCheck for every spec, each with its own timeout.
func runChecks(ctx context.Context, l api.Local, node string, idx []int, specs []ports.Spec) wizChecked {
	out := wizChecked{idx: idx, res: make([]portCheck, len(specs))}
	for i, s := range specs {
		cctx, cancel := context.WithTimeout(ctx, checkTimeout)
		res, err := l.PortCheck(cctx, api.PortCheckRequest{Port: s.Listen, Proto: s.Proto, Node: node})
		cancel()
		pc := portCheck{spec: s, err: err}
		if err == nil {
			pc.res = &res
		}
		out.res[i] = pc
	}
	return out
}

// canStop reports whether the busy port belongs to a deyroute tunnel, the
// only kind of service the wizard may stop (section 10).
func (w *wizard) canStop() (string, bool) {
	c := w.checks[w.cur]
	if c.res != nil && !c.res.BindFree && c.res.BindByDey && c.res.Tunnel != "" {
		return c.res.Tunnel, true
	}
	return "", false
}

func (w *wizard) keyResolve(a *app, k tea.KeyMsg) tea.Cmd {
	if submit, _ := w.ch.key(k); !submit {
		return nil
	}
	n, raw, ok := w.ch.take()
	w.msg, w.inputErr = "", nil
	tunnel, stoppable := w.canStop()
	switch {
	case ok && n == 0:
		w.setStep(wzPorts)
	case ok && n == 1:
		w.input = ""
		w.setStep(wzNewPort)
	case ok && n == 2:
		w.checks = slices.Delete(w.checks, w.cur, w.cur+1)
		if len(w.checks) == 0 {
			w.msg = i18n.T(i18n.TUIWizNoPortsLeft)
			w.input = ""
			w.setStep(wzPorts)
			return nil
		}
		return w.afterCheck(a)
	case ok && n == 3 && stoppable:
		spec := w.checks[w.cur].spec.String()
		return a.push(newConfirm(i18n.T(i18n.TUITunAdd), i18n.T(i18n.TUIWizStopConfirm, tunnel, spec), true,
			func(a *app) tea.Cmd {
				return tea.Batch(a.pop(), w.stopService(a, tunnel))
			}))
	default:
		if raw != "" {
			w.msg = i18n.T(i18n.InvalidChoice, raw)
		}
	}
	return nil
}

// stopService disables the deyroute tunnel holding the port, then re-checks it.
func (w *wizard) stopService(a *app, tunnel string) tea.Cmd {
	w.setStep(wzChecking)
	node, cur, spec := w.node.ID, w.cur, w.checks[w.cur].spec
	return a.call(w, 0, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		cctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		if err := l.TunnelSetEnabled(cctx, tunnel, false); err != nil {
			return nil, err
		}
		return runChecks(ctx, l, node, []int{cur}, []ports.Spec{spec}), nil
	})
}

func (w *wizard) submitNewPort(a *app) tea.Cmd {
	raw := strings.TrimSpace(w.input)
	n, err := strconv.Atoi(raw)
	if err != nil || !ports.ValidPort(n) {
		w.inputErr = deyerr.New(deyerr.P010, deyerr.Params{"input": raw})
		return nil
	}
	old := w.checks[w.cur].spec
	ns := old
	ns.Listen = n
	if old.Target == "" || old.Target == ports.DefaultTarget(old.Listen) {
		ns.Target = ports.DefaultTarget(n)
	}
	for i, c := range w.checks {
		if i != w.cur && c.spec.Listen == n && c.spec.Proto == ns.Proto {
			w.inputErr = uiErr(i18n.TUIWizDupPort, ns.String())
			return nil
		}
	}
	w.inputErr = nil
	w.input = ""
	w.checks[w.cur] = portCheck{spec: ns}
	return w.check(a, []int{w.cur})
}

func (w *wizard) keyConfirm(a *app, k tea.KeyMsg) tea.Cmd {
	if submit, _ := w.ch.key(k); !submit {
		return nil
	}
	n, raw, ok := w.ch.take()
	w.msg = ""
	switch {
	case raw == "" || (ok && n == 1):
		return w.create(a)
	case ok && n == 2 && a.advanced:
		return w.openAdvanced(a)
	case ok && n == 0:
		w.setStep(wzPorts)
	default:
		w.msg = i18n.T(i18n.InvalidChoice, raw)
	}
	return nil
}

// specs returns the checked ports as API port specs.
func (w *wizard) specs() []api.PortSpec {
	out := make([]api.PortSpec, len(w.checks))
	for i, c := range w.checks {
		out[i] = api.PortSpec{Listen: c.spec.Listen, Proto: c.spec.Proto, Target: c.spec.Target}
	}
	return out
}

// defaultLadder is the ladder the daemon picks by protocol (section 8).
func (w *wizard) defaultLadder() (string, []string) {
	for _, c := range w.checks {
		if c.spec.Proto != config.ProtoUDP {
			return config.DefaultLadderName, config.DefaultLadder
		}
	}
	return config.DefaultUDPLadderName, config.DefaultUDPLadder
}

// customRungs returns the edited ladder when it differs from the default.
func (w *wizard) customRungs() []string {
	_, def := w.defaultLadder()
	if len(w.rungs) == 0 || slices.Equal(w.rungs, def) {
		return nil
	}
	return w.rungs
}

func (w *wizard) request() api.TunnelAddRequest {
	req := api.TunnelAddRequest{Node: w.node.ID, Ports: w.specs()}
	req.Name = w.name
	req.Rungs = w.customRungs()
	if w.backup != "" {
		req.Backups = []string{w.backup}
	}
	req.Policy = w.policy
	req.TLSMode = w.tls
	req.Failover = w.failover
	return req
}

func (w *wizard) create(a *app) tea.Cmd {
	req := w.request()
	tried := false
	task := newTask(i18n.T(i18n.TUITunAdd), longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
		if tried {
			// Retry: a failed step after the tunnel was saved leaves it
			// configured (its ports are taken), so it is restarted
			// instead of being added a second time (DEY-C003).
			if id, ok := addedTunnel(ctx, l, req); ok {
				if err := l.TunnelRestart(ctx, id); err != nil {
					return nil, err
				}
				d, err := l.TunnelShow(ctx, id)
				return d.TunnelInfo, err
			}
		}
		tried = true
		return l.TunnelAdd(ctx, req, progress)
	}, renderTunnelUp)
	return a.replace(task)
}

// addedTunnel finds the tunnel a failed TunnelAdd of req saved anyway: the
// one that holds req's first listen port (ports are unique on the hub).
func addedTunnel(ctx context.Context, l api.Local, req api.TunnelAddRequest) (string, bool) {
	if len(req.Ports) == 0 {
		return "", false
	}
	want := req.Ports[0]
	proto := want.Proto
	if proto == "" {
		proto = "tcp"
	}
	ts, err := l.TunnelList(ctx)
	if err != nil {
		return "", false
	}
	for _, t := range ts {
		for _, p := range t.Ports {
			if p.Listen == want.Listen && (p.Proto == proto || p.Proto == "" && proto == "tcp") {
				return t.ID, true
			}
		}
	}
	return "", false
}

// renderTunnelUp is the last line of the progress screen:
// "Tunnel main is UP via backhaul/wssmux (41ms)".
func renderTunnelUp(a *app, v any) string {
	t, _ := v.(api.TunnelInfo)
	if t.State == stUp {
		return a.paint(colGreen, " "+i18n.T(i18n.TUITunnelUp, t.ID, t.ActiveTransport, t.RTTms)) + "\n"
	}
	return a.paint(colYellow, " "+i18n.T(i18n.TUITunnelCreated, t.ID, a.stateText(t))) + "\n"
}

// openAdvanced shows the ladder editor, then the options form.
func (w *wizard) openAdvanced(a *app) tea.Cmd {
	name, def := w.defaultLadder()
	rungs := w.rungs
	if len(rungs) == 0 {
		rungs = def
	}
	title := i18n.T(i18n.TUILadTitle, name)
	return a.push(newLadderEditor(title, rungs, func(a *app, r []string) tea.Cmd {
		w.rungs = r
		return a.replace(w.advForm())
	}))
}

// otherOnline lists online nodes other than the chosen one.
func (w *wizard) otherOnline() []string {
	var ids []string
	for _, n := range w.nodes {
		if n.ID != w.node.ID {
			ids = append(ids, n.ID)
		}
	}
	return ids
}

func (w *wizard) advForm() *formScreen {
	others := w.otherOnline()
	avail := strings.Join(others, ", ")
	if avail == "" {
		avail = i18n.T(i18n.TUINone)
	}
	fields := []field{{key: "name", label: i18n.T(i18n.TUIWizName), def: w.name, optional: true}}
	for i, c := range w.checks {
		def := c.spec.Target
		if def == "" {
			def = ports.DefaultTarget(c.spec.Listen)
		}
		fields = append(fields, field{key: "target" + strconv.Itoa(i), label: i18n.T(i18n.TUIWizTarget, c.spec.String()), def: def, check: checkTarget})
	}
	fields = append(fields,
		field{key: "backup", label: i18n.T(i18n.TUIWizBackupQ, avail), def: w.backup, optional: true,
			check: func(v string, _ map[string]string) error {
				if v == "" || slices.Contains(others, v) {
					return nil
				}
				return uiErr(i18n.TUIWizUnknownNode, v)
			}},
		field{key: "policy", label: i18n.T(i18n.TUIEditPolicy), def: orDefault(w.policy, config.PolicyTransportThenNode),
			check: checkOneOf(config.PolicyTransportThenNode, config.PolicyTransportOnly, config.PolicyNodeOnly)},
		// custom needs certificate paths, which TunnelAdd does not take:
		// it is set afterwards with Edit tunnel.
		field{key: "tls", label: i18n.T(i18n.TUIWizTLS), def: orDefault(w.tls, config.TLSModeAuto),
			check: checkOneOf(config.TLSModeAuto, config.TLSModeACME)},
		field{key: "thresh", label: i18n.T(i18n.TUIWizThreshQ), def: "n", check: checkYes},
	)
	fields = append(fields, thresholdFields(w.failover, func(v map[string]string) bool {
		y, _ := parseYes(v["thresh"])
		return !y
	})...)
	return newForm(i18n.T(i18n.TUITunAdd), i18n.T(i18n.TUIWizAdvIntro), fields, func(a *app, v map[string]string) tea.Cmd {
		w.name = v["name"]
		for i := range w.checks {
			w.checks[i].spec.Target = v["target"+strconv.Itoa(i)]
		}
		w.backup, w.policy, w.tls = v["backup"], v["policy"], v["tls"]
		w.failover = nil
		if y, _ := parseYes(v["thresh"]); y {
			w.failover = thresholdsFrom(v)
		}
		return a.pop()
	})
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// checkTarget validates a host:port target (DEY-C004).
func checkTarget(v string, _ map[string]string) error {
	host, port, err := net.SplitHostPort(v)
	if err == nil && host != "" {
		if n, perr := strconv.Atoi(port); perr == nil && ports.ValidPort(n) {
			return nil
		}
	}
	return deyerr.New(deyerr.C004, deyerr.Params{"target": v, "tunnel": i18n.T(i18n.TUIWizNewTunnel)})
}

// thresholdFields are the failover threshold questions (Advanced), with the
// current values (or the section 9 defaults) in brackets.
func thresholdFields(cur *api.FailoverSettings, skip func(map[string]string) bool) []field {
	f := defaultFailover()
	if cur != nil {
		f = *cur
	}
	num := func(key string, label i18n.Key, v, min int) field {
		return field{key: key, label: i18n.T(label), def: strconv.Itoa(v), check: checkInt(min), skip: skip}
	}
	fb := "n"
	if f.Failback {
		fb = "y"
	}
	return []field{
		num("probe_interval", i18n.TUIThProbeInterval, f.ProbeIntervalS, 1),
		num("probe_timeout", i18n.TUIThProbeTimeout, f.ProbeTimeoutS, 1),
		num("fail", i18n.TUIThFail, f.FailThreshold, 1),
		num("recover", i18n.TUIThRecover, f.RecoverThreshold, 1),
		{key: "failback", label: i18n.T(i18n.TUIThFailback), def: fb, check: checkYes, skip: skip},
		num("failback_after", i18n.TUIThFailbackAfter, f.FailbackAfterS, 0),
		num("max_switches", i18n.TUIThMaxSwitches, f.MaxSwitchesPerHour, 1),
		num("quarantine", i18n.TUIThQuarantine, f.QuarantineS, 0),
	}
}

func defaultFailover() api.FailoverSettings {
	return api.FailoverSettings{
		Policy:             config.PolicyTransportThenNode,
		ProbeIntervalS:     config.DefaultProbeIntervalS,
		ProbeTimeoutS:      config.DefaultProbeTimeoutS,
		FailThreshold:      config.DefaultFailThreshold,
		RecoverThreshold:   config.DefaultRecoverThresh,
		Failback:           true,
		FailbackAfterS:     config.DefaultFailbackAfterS,
		MaxSwitchesPerHour: config.DefaultMaxSwitchesHour,
		QuarantineS:        config.DefaultQuarantineS,
	}
}

func thresholdsFrom(v map[string]string) *api.FailoverSettings {
	fb, _ := parseYes(v["failback"])
	return &api.FailoverSettings{
		Policy:             v["policy"],
		ProbeIntervalS:     atoi(v["probe_interval"]),
		ProbeTimeoutS:      atoi(v["probe_timeout"]),
		FailThreshold:      atoi(v["fail"]),
		RecoverThreshold:   atoi(v["recover"]),
		Failback:           fb,
		FailbackAfterS:     atoi(v["failback_after"]),
		MaxSwitchesPerHour: atoi(v["max_switches"]),
		QuarantineS:        atoi(v["quarantine"]),
	}
}

// ---- view

func (w *wizard) view(a *app) string {
	var b strings.Builder
	// 1. node
	b.WriteString(" " + a.bold(i18n.T(i18n.TUIWizQNode)) + "\n")
	switch {
	case w.loadErr != nil:
		b.WriteString(a.errBlock(w.loadErr))
		return b.String()
	case w.loading:
		b.WriteString("  " + i18n.T(i18n.Loading) + "\n")
		return b.String()
	case len(w.nodes) == 0:
		b.WriteString(a.paint(colYellow, indent(i18n.T(i18n.TUIWizNoNode))) + "\n")
		return b.String()
	case w.auto:
		b.WriteString("  " + i18n.T(i18n.TUIWizAutoNode, a.nodeName(w.node)) + "\n")
	case w.step == wzNode:
		for i, n := range w.nodes {
			b.WriteString(" " + numLine(i+1, a.nodeLabel(n)) + "\n")
		}
		b.WriteString(" " + numLine(0, i18n.T(i18n.TUIBackItem)) + "\n")
		w.writeMsg(a, &b)
		b.WriteString("\n " + i18n.T(i18n.PromptChoice) + w.ch.input + "\n")
		return b.String()
	default:
		b.WriteString("  " + a.nodeName(w.node) + "\n")
	}
	// 2. ports
	b.WriteString("\n " + a.bold(i18n.T(i18n.TUIWizQPorts)) + "\n")
	if w.step == wzPorts {
		b.WriteString(a.paint(colGray, "  "+i18n.T(i18n.TUIWizPortsHint)) + "\n")
		b.WriteString("  " + i18n.T(i18n.TUIWizPortsPrompt) + ": " + w.input + "_\n")
		if w.inputErr != nil {
			b.WriteString("\n" + a.errBlock(w.inputErr))
		}
		w.writeMsg(a, &b)
		return b.String()
	}
	for _, c := range w.checks {
		b.WriteString(w.checkLine(a, c) + "\n")
	}
	switch w.step {
	case wzChecking:
		b.WriteString("  " + i18n.T(i18n.TUIWizChecking) + "\n")
	case wzResolve, wzNewPort:
		b.WriteString(w.resolveView(a))
	case wzConfirm:
		b.WriteString("\n " + a.bold(i18n.T(i18n.TUIWizQConfirm)) + "\n")
		b.WriteString(w.summary(a))
		b.WriteString("\n " + i18n.T(i18n.TUIWizCreate) + "\n")
		b.WriteString(" " + numLine(1, i18n.T(i18n.TUIWizCreateItem)) + "\n")
		if a.advanced {
			b.WriteString(" " + numLine(2, i18n.T(i18n.TUIWizAdvOptions)) + "\n")
		}
		b.WriteString(" " + numLine(0, i18n.T(i18n.TUIBackItem)) + "\n")
		w.writeMsg(a, &b)
		b.WriteString("\n " + i18n.T(i18n.PromptChoice) + w.ch.input + "\n")
	}
	return b.String()
}

func (w *wizard) writeMsg(a *app, b *strings.Builder) {
	if w.msg != "" {
		b.WriteString("\n" + a.paint(colYellow, " "+w.msg) + "\n")
	}
}

func (a *app) nodeName(n api.NodeInfo) string {
	if n.Name != "" && n.Name != n.ID {
		return n.ID + " (" + n.Name + ")"
	}
	return n.ID
}

// checkLine is one checked port: "443/tcp is free ✔" or
// "443/tcp is used by nginx (pid 1234)".
func (w *wizard) checkLine(a *app, c portCheck) string {
	s := a.sym()
	spec := c.spec.String()
	switch {
	case c.err != nil:
		return a.paint(colRed, "  "+s.fail+" "+i18n.T(i18n.TUIWizPortError, spec))
	case c.res == nil:
		return "  " + s.run + " " + spec
	case c.res.BindFree:
		line := "  " + a.paint(colGreen, s.ok) + " " + i18n.T(i18n.TUIWizPortFree, spec)
		// Stages 2 and 3 do not block the tunnel, but the owner must see
		// them: a closed firewall or provider panel stops the users.
		if !c.res.FirewallOpen && c.res.FirewallName != "" {
			w := i18n.T(i18n.TUIWizPortFirewall, c.res.FirewallName)
			if c.res.FirewallCommand != "" {
				w += " " + i18n.T(i18n.TUIWizPortRun, c.res.FirewallCommand)
			}
			line += "\n" + a.paint(colYellow, "    "+s.warn+" "+w)
		}
		if c.res.NodeReachable != nil && !*c.res.NodeReachable {
			line += "\n" + a.paint(colYellow, "    "+s.warn+" "+i18n.T(i18n.TUIWizPortUnreachable, c.res.Node))
		}
		return line
	case c.res.BindProcess != "":
		return a.paint(colRed, "  "+s.fail+" "+i18n.T(i18n.TUIWizPortBusy, spec, c.res.BindProcess))
	}
	return a.paint(colRed, "  "+s.fail+" "+i18n.T(i18n.TUIWizPortBusyAny, spec))
}

func (w *wizard) resolveView(a *app) string {
	var b strings.Builder
	c := w.checks[w.cur]
	b.WriteString("\n")
	if c.err != nil {
		b.WriteString(a.errBlock(c.err))
	}
	if w.step == wzNewPort {
		if c.res != nil && len(c.res.SuggestedPorts) > 0 {
			var ss []string
			for _, p := range c.res.SuggestedPorts {
				ss = append(ss, strconv.Itoa(p))
			}
			b.WriteString(a.paint(colGray, "  "+i18n.T(i18n.TUIWizSuggest, strings.Join(ss, ", "))) + "\n")
		}
		b.WriteString("  " + i18n.T(i18n.TUIWizNewPort, c.spec.String()) + ": " + w.input + "_\n")
		if w.inputErr != nil {
			b.WriteString("\n" + a.errBlock(w.inputErr))
		}
		return b.String()
	}
	if w.inputErr != nil {
		b.WriteString(a.errBlock(w.inputErr) + "\n")
	}
	b.WriteString(" " + numLine(1, i18n.T(i18n.TUIWizChange)) + "\n")
	b.WriteString(" " + numLine(2, i18n.T(i18n.TUIWizSkip)) + "\n")
	if t, ok := w.canStop(); ok {
		b.WriteString(" " + numLine(3, i18n.T(i18n.TUIWizStop, t)) + "\n")
	}
	b.WriteString(" " + numLine(0, i18n.T(i18n.TUIBackItem)) + "\n")
	w.writeMsg(a, &b)
	b.WriteString("\n " + i18n.T(i18n.PromptChoice) + w.ch.input + "\n")
	return b.String()
}

// summary is the confirmation: node, ports, default ladder, backup: none.
func (w *wizard) summary(a *app) string {
	var b strings.Builder
	var ps []string
	for _, c := range w.checks {
		p := c.spec.String()
		if c.spec.Target != "" && c.spec.Target != ports.DefaultTarget(c.spec.Listen) {
			p += " " + a.sym().arrow + " " + c.spec.Target
		}
		ps = append(ps, p)
	}
	name, _ := w.defaultLadder()
	ladder := i18n.T(i18n.TUIWizDefault, name)
	if r := w.customRungs(); r != nil {
		ladder = strings.Join(r, " "+a.sym().arrow+" ")
	}
	backup := orDefault(w.backup, i18n.T(i18n.TUINone))
	if a.advanced {
		b.WriteString(kv(i18n.T(i18n.TUIWizSumName), orDefault(w.name, i18n.T(i18n.TUIWizAutomatic))))
	}
	b.WriteString(kv(i18n.T(i18n.TUIWizSumNode), a.nodeName(w.node)))
	b.WriteString(kv(i18n.T(i18n.TUIWizSumPorts), strings.Join(ps, ", ")))
	b.WriteString(kv(i18n.T(i18n.TUIWizSumLadder), ladder))
	b.WriteString(kv(i18n.T(i18n.TUIWizSumBackup), backup))
	if a.advanced {
		b.WriteString(kv(i18n.T(i18n.TUIWizSumPolicy), orDefault(w.policy, config.PolicyTransportThenNode)))
		b.WriteString(kv(i18n.T(i18n.TUIWizSumTLS), orDefault(w.tls, config.TLSModeAuto)))
		th := i18n.T(i18n.TUIWizDefaultWord)
		if w.failover != nil {
			th = i18n.T(i18n.TUIWizCustom)
		}
		b.WriteString(kv(i18n.T(i18n.TUIWizSumThresh), th))
	}
	return b.String()
}
