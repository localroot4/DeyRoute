package setup

import (
	"context"
	stderrors "errors"
	"log/slog"
	"strings"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	deylog "github.com/localroot4/deyroute/internal/log"
)

// Step ids reported through the Progress callbacks (api.Step.ID).
const (
	// SetupHub.
	StepDetectIP = "detect_ip"
	StepCA       = "ca"
	StepHubCert  = "hub_cert"
	StepFirewall = "firewall"
	StepSysctl   = "sysctl"
	StepConfig   = "config"
	StepService  = "service"

	// Join (also StepSysctl, StepConfig, StepService).
	StepParseLink = "parse_link"
	StepKeys      = "keys"
	StepJoin      = "join"
	StepSecrets   = "secrets"

	// Uninstall.
	StepStopUnits      = "stop_units"
	StepUnitFiles      = "unit_files"
	StepFirewallRemove = "firewall_remove"
	StepSysctlRevert   = "sysctl_revert"
	StepFiles          = "files"
	StepBinary         = "binary"

	// Restore (also StepSysctl, StepService).
	StepRestore = "restore"
	StepEvents  = "events"
)

// StepTitleKeyPrefix is the i18n key prefix of step titles:
// "setup.step.<id>".
const StepTitleKeyPrefix = "setup.step."

// defaultTitles are used while internal/i18n has no "setup.step.<id>" key.
var defaultTitles = map[string]string{
	StepDetectIP:       "Detect public IP",
	StepCA:             "Create internal CA",
	StepHubCert:        "Issue hub certificate",
	StepFirewall:       "Apply firewall (table inet deyroute)",
	StepSysctl:         "Apply kernel settings",
	StepConfig:         "Write /etc/deyroute/config.yaml",
	StepService:        "Install and start service",
	StepParseLink:      "Read join link",
	StepKeys:           "Create node key",
	StepJoin:           "Join hub",
	StepSecrets:        "Save certificates",
	StepStopUnits:      "Stop and disable services",
	StepUnitFiles:      "Remove unit files",
	StepFirewallRemove: "Remove firewall table inet deyroute",
	StepSysctlRevert:   "Restore kernel settings",
	StepFiles:          "Remove files",
	StepBinary:         "Remove deyroute binary",
	StepRestore:        "Restore backup",
	StepEvents:         "Save events for import",
}

// StepTitle returns the human title of a step id: the i18n string
// "setup.step.<id>" when it exists, otherwise the English default.
func StepTitle(id string) string {
	k := i18n.Key(StepTitleKeyPrefix + id)
	if s := i18n.T(k); s != string(k) {
		return s
	}
	if s, ok := defaultTitles[id]; ok {
		return s
	}
	return id
}

// reporter sends api.Step updates to an optional callback and the log.
type reporter struct {
	progress func(api.Step)
	log      *slog.Logger
}

func newReporter(progress func(api.Step), logger *slog.Logger) *reporter {
	if logger == nil {
		logger = deylog.Discard()
	}
	return &reporter{progress: progress, log: logger}
}

func (r *reporter) emit(st api.Step) {
	st.Title = StepTitle(st.ID)
	st.Detail = deylog.Redact(st.Detail)
	if st.Error != nil {
		// The step leaves this process (progress screens, NDJSON) and its
		// cause may quote command output or a pasted join link.
		d := *st.Error
		d.Message, d.Why, d.Fix, d.Detail = deylog.Redact(d.Message), deylog.Redact(d.Why), deylog.Redact(d.Fix), deylog.Redact(d.Detail)
		st.Error = &d
	}
	lvl := slog.LevelInfo
	switch st.Status {
	case api.StepFailed:
		lvl = slog.LevelError
	case api.StepWarn:
		lvl = slog.LevelWarn
	}
	attrs := []any{slog.String("step", st.ID), slog.String("status", st.Status)}
	if st.Detail != "" {
		attrs = append(attrs, slog.String("detail", st.Detail))
	}
	if st.Error != nil {
		attrs = append(attrs, slog.String(deylog.KeyCode, st.Error.Code))
	}
	r.log.Log(context.Background(), lvl, st.Title, attrs...)
	if r.progress != nil {
		r.progress(st)
	}
}

func (r *reporter) start(id string) { r.emit(api.Step{ID: id, Status: api.StepRunning}) }

func (r *reporter) ok(id, detail string) {
	r.emit(api.Step{ID: id, Status: api.StepOK, Detail: detail})
}

func (r *reporter) skip(id, detail string) {
	r.emit(api.Step{ID: id, Status: api.StepSkipped, Detail: detail})
}

func (r *reporter) warn(id, detail string, err error) {
	r.emit(api.Step{ID: id, Status: api.StepWarn, Detail: detail, Error: api.ToDTO(err)})
}

// okOrWarn reports ok, or warn carrying warning when it is not nil.
func (r *reporter) okOrWarn(id, detail string, warning error) {
	if warning != nil {
		r.warn(id, detail, warning)
		return
	}
	r.ok(id, detail)
}

// fail reports a failed step. The step carries the root cause (not the
// DEY-I014/I022 wrapper the operation returns) so the progress screen
// shows the specific code.
func (r *reporter) fail(id string, cause error) {
	r.emit(api.Step{ID: id, Status: api.StepFailed, Error: api.ToDTO(cause)})
}

// stepError wraps cause as code {step} (DEY-I014 for setup/restore steps,
// DEY-I022 for uninstall steps) and puts the cause's lines in Detail, so
// the canonical error block shows what actually failed.
func stepError(code deyerr.Code, step string, cause error) *deyerr.Error {
	return deyerr.Wrap(code, cause, deyerr.Params{"step": step}).WithDetail(describe(cause))
}

// describe renders every DEY error inside err (joined errors included) as
// "CODE message / Why / Fix / detail" lines, redacted.
func describe(err error) string {
	var parts []string
	for _, e := range flatten(err) {
		de := deyerr.As(e)
		var b strings.Builder
		b.WriteString(string(de.Code) + " " + de.Message())
		if de.Code == deyerr.X000 && de.Cause != nil {
			b.WriteString(": " + de.Cause.Error())
		}
		b.WriteString("\nWhy: " + de.Why())
		b.WriteString("\nFix: " + de.Fix())
		if d := strings.TrimSpace(de.Detail); d != "" {
			b.WriteString("\n" + d)
		}
		parts = append(parts, b.String())
	}
	return deylog.Redact(strings.Join(parts, "\n"))
}

// flatten splits errors.Join values into their parts.
func flatten(err error) []error {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var out []error
		for _, e := range j.Unwrap() {
			out = append(out, flatten(e)...)
		}
		return out
	}
	return []error{err}
}

// isDEY reports whether err carries a DEY code (possibly inside a join).
func isDEY(err error) bool {
	var de *deyerr.Error
	return stderrors.As(err, &de)
}

// serviceFailed points the Fix line of a failed service step at the unit:
// config.yaml already exists, so re-running setup would be refused.
func serviceFailed(err error, unit string) error {
	var de *deyerr.Error
	if stderrors.As(err, &de) {
		return de.WithFix("fix the cause shown below, then start the service: systemctl enable --now " + unit +
			"   (status: systemctl status " + unit + ")")
	}
	return err
}
