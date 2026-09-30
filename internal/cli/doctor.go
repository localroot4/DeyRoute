package cli

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/doctor"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
)

// DoctorTimeout bounds the whole doctor run (collection on hub and node).
const DoctorTimeout = 5 * time.Minute

// maxSecretFile bounds the secret files read for registration.
const maxSecretFile = 1 << 20

func newDoctorCmd(g *Globals) *cobra.Command {
	var node, out string
	cmd := &cobra.Command{
		Use:     "doctor",
		Short:   i18n.T(i18n.CLIDoctorShort),
		Long:    i18n.T(i18n.CLIDoctorLong),
		Example: i18n.T(i18n.CLIDoctorExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), DoctorTimeout)
			defer cancel()
			if out != "" {
				abs, err := filepath.Abs(out)
				if err != nil {
					return usageErr(err.Error())
				}
				out = abs
			}
			res, err := g.doctorRun(ctx, strings.TrimSpace(node), out, !g.JSON && g.caps().Color)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{
					"path": res.Path, "role": res.Role, "daemon_running": res.DaemonRunning,
					"node": node, "findings": nonNil(res.Findings),
				})
			}
			if !res.DaemonRunning {
				g.say(i18n.CLIDoctorLocalOnly, g.service())
			}
			g.printf("%s", g.text(res.Summary))
			g.say(i18n.CLIDoctorWritten, res.Path)
			return nil
		},
	}
	cmd.Flags().StringVar(&node, "node", "", i18n.T(i18n.CLIFlagDoctorNode))
	cmd.Flags().StringVar(&out, "out", "", i18n.T(i18n.CLIFlagDoctorOut))
	return cmd
}

// doctorResult is the outcome of one doctor run.
type doctorResult struct {
	Summary       string
	Path          string
	Role          string
	Findings      []api.DoctorFinding
	DaemonRunning bool
}

// doctorRun is `deyroute doctor` (spec section 13), shared by the CLI and the
// TUI: every secret on disk is registered with the central filter first so
// the bundle scan catches it; the daemon collects the hub (and, with node,
// that node) sections and findings; without a daemon the local collector
// and the rules run in-process. The summary (at most 20 lines) and the
// redacted bundle (/root/deyroute-doctor-<UTC>.tar.gz or out) are produced
// here, by the CLI process, because the daemon cannot write to /root.
func (g *Globals) doctorRun(ctx context.Context, node, out string, color bool) (doctorResult, error) {
	registerSecrets(g.Root, g.localConfig())
	res := doctorResult{Role: g.role()}
	var data api.DoctorData
	var st api.Status
	l, err := g.local()
	if err == nil {
		cctx, cancel := context.WithTimeout(ctx, DoctorTimeout)
		data, err = l.DoctorCollect(cctx, "")
		cancel()
	}
	switch {
	case err == nil:
		res.DaemonRunning = true
	case errDaemonDown(err) && node == "":
	case deyerr.HasCode(err, deyerr.X008) && node == "":
		// A daemon without doctor support: collect locally.
		res.DaemonRunning = true
	default:
		return res, err
	}
	if res.DaemonRunning {
		sctx, cancel := callCtx(ctx)
		if s, serr := l.Status(sctx); serr == nil {
			st = s
		}
		cancel()
	}
	if err != nil {
		data = g.localDoctor(ctx, st)
	}
	if data.Role != "" {
		res.Role = data.Role
	}
	findings := append([]api.DoctorFinding(nil), data.Findings...)
	parts := doctor.SectionParts(data.Sections, "")
	if node != "" {
		cctx, cancel := context.WithTimeout(ctx, DoctorTimeout)
		nd, err := l.DoctorCollect(cctx, node)
		cancel()
		if err != nil {
			return res, err
		}
		for _, f := range nd.Findings {
			f.Message = node + ": " + f.Message
			findings = append(findings, f)
		}
		for k, v := range doctor.SectionParts(nd.Sections, doctor.NodePrefix(node)) {
			parts[k] = v
		}
	}
	findings = doctor.SortFindings(findings)
	res.Findings = findings
	if st.Role == "" {
		st.Role = res.Role
	}
	res.Summary = doctor.Summary(findings, st, g.unicode(), color)
	now := g.Now().UTC()
	if out != "" {
		res.Path, err = doctor.WriteBundleFile(out, now, parts, findings, res.Summary)
		return res, err
	}
	dir := g.path(doctor.DefaultBundleDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return res, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	res.Path, err = doctor.WriteBundle(dir, now, parts, findings, res.Summary)
	return res, err
}

// localDoctor collects this server's sections and runs the rules in-process:
// the daemon is down (rule R06 reports that) or cannot collect; st is its
// status when it answered one.
func (g *Globals) localDoctor(ctx context.Context, st api.Status) api.DoctorData {
	c := &doctor.Collector{Root: g.Root, Runner: g.Runner, Now: g.Now}
	if cfg := g.localConfig(); cfg != nil {
		for _, t := range cfg.Tunnels {
			if t.TLS.CertFile != "" {
				if c.TunnelCertFiles == nil {
					c.TunnelCertFiles = map[string]string{}
				}
				c.TunnelCertFiles[t.ID] = t.TLS.CertFile
			}
		}
	}
	col := c.Collect(ctx)
	if st.Role == "" {
		st.Role = g.role()
	}
	f := doctor.Facts{Role: g.role(), Now: g.Now(), Status: st}
	col.Apply(&f)
	sections := col.Sections
	if f.Sections != nil {
		sections = f.Sections
	}
	return api.DoctorData{Role: g.role(), Sections: sections, Findings: doctor.Run(f)}
}

// registerSecrets reads every token, key and password file under
// Root/etc/deyroute/secrets (and the Telegram token file named in the config)
// and registers the values with the central redaction filter, so the
// bundle's final scan also catches a bare secret that some log line kept.
// Unreadable files are skipped: doctor must work on a damaged server.
func registerSecrets(root string, cfg *config.Config) {
	dir := rootPath(root, config.SecretsDir)
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() || publicSecretFile(d.Name()) {
			return nil
		}
		registerFile(p)
		return nil
	})
	if cfg != nil && cfg.Hub != nil {
		if f := cfg.Hub.Notify.Telegram.BotTokenFile; f != "" {
			registerFile(rootPath(root, f))
		}
	}
}

// publicSecretFile reports files of the secrets directory that hold no
// secret: certificates and checksums (registering them would only mask
// public data in the bundle).
func publicSecretFile(name string) bool {
	switch {
	case strings.HasSuffix(name, ".crt"), name == "cert.pem", name == "tls-cert.pem",
		strings.HasSuffix(name, "-sha256"), strings.HasSuffix(name, ".p12"), name == "join-tokens.json":
		return true
	}
	return false
}

// registerFile registers the content of one secret file: every string
// value of a JSON document, otherwise the whole trimmed text.
func registerFile(p string) {
	fi, err := os.Stat(p)
	if err != nil || fi.Size() > maxSecretFile {
		return
	}
	data, err := os.ReadFile(p) // #nosec G304 -- files below the secrets directory
	if err != nil {
		return
	}
	var doc any
	if strings.HasSuffix(p, ".json") && json.Unmarshal(data, &doc) == nil {
		registerJSON(doc)
		return
	}
	dlog.RegisterSecret(string(data))
}

func registerJSON(v any) {
	switch x := v.(type) {
	case map[string]any:
		for _, e := range x {
			registerJSON(e)
		}
	case []any:
		for _, e := range x {
			registerJSON(e)
		}
	case string:
		if _, err := time.Parse(time.RFC3339Nano, x); err != nil {
			dlog.RegisterSecret(x)
		}
	}
}
