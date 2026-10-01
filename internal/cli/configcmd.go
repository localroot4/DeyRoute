package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/node"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
)

// editHeaderPrefix starts every line `config edit` adds on top of the file
// (the validation errors); they are removed again before saving.
const editHeaderPrefix = "# deyroute: "

// maxEditRounds bounds the edit → validate loop.
const maxEditRounds = 100

// EditorTimeout bounds one editor session of `config edit`.
const EditorTimeout = 12 * time.Hour

// validateOptions validates with every registered transport.
func validateOptions() config.ValidateOptions {
	return config.ValidateOptions{KnownTransport: backend.KnownTransport, ValidTransports: backend.ValidIDs}
}

func newConfigCmd(g *Globals) *cobra.Command {
	cmd := newGroup("config", i18n.CLIConfigShort)
	cmd.AddCommand(
		&cobra.Command{
			Use:     "show",
			Short:   i18n.T(i18n.CLIConfigShowShort),
			Example: i18n.T(i18n.CLIConfigShowExample),
			Args:    noArgs(),
			RunE: func(*cobra.Command, []string) error {
				data, err := g.readConfig()
				if err != nil {
					return err
				}
				if g.JSON {
					var doc map[string]any
					if err := yaml.Unmarshal(data, &doc); err != nil {
						return deyerr.Wrap(deyerr.C014, err, deyerr.Params{"path": config.DefaultPath})
					}
					// The central secret filter applies to every output
					// (spec sections 11 and 13), JSON included.
					return g.emitJSON(map[string]any{"path": config.DefaultPath, "config": redactValues(doc)})
				}
				g.printf("%s", dlog.Redact(string(data)))
				if len(data) > 0 && data[len(data)-1] != '\n' {
					g.println()
				}
				return nil
			},
		},
		&cobra.Command{
			Use:     "validate",
			Short:   i18n.T(i18n.CLIConfigValidateShort),
			Example: i18n.T(i18n.CLIConfigValidateExample),
			Args:    noArgs(),
			RunE: func(*cobra.Command, []string) error {
				data, err := g.readConfig()
				if err != nil {
					return err
				}
				c, err := config.ParseWith(data, validateOptions())
				if err != nil {
					return err
				}
				if g.JSON {
					return g.emitJSON(map[string]any{"path": config.DefaultPath, "valid": true, "role": c.Role,
						"tunnels": len(c.Tunnels), "nodes": len(c.Nodes)})
				}
				if c.Role == config.RoleHub {
					g.say(i18n.CLIConfigValidHub, config.DefaultPath, len(c.Tunnels), len(c.Nodes))
				} else {
					g.say(i18n.CLIConfigValid, config.DefaultPath, c.Role)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:     "edit",
			Short:   i18n.T(i18n.CLIConfigEditShort),
			Long:    i18n.T(i18n.CLIConfigEditLong),
			Example: i18n.T(i18n.CLIConfigEditExample),
			Args:    noArgs(),
			RunE: func(cmd *cobra.Command, _ []string) error {
				return g.configEdit(cmd.Context())
			},
		},
		&cobra.Command{
			Use:     "apply",
			Short:   i18n.T(i18n.CLIConfigApplyShort),
			Example: i18n.T(i18n.CLIConfigApplyExample),
			Args:    noArgs(),
			RunE: func(cmd *cobra.Command, _ []string) error {
				l, err := g.local()
				if err != nil {
					return err
				}
				return g.configApply(cmd.Context(), l, nil)
			},
		},
	)
	return cmd
}

// redactValues masks secrets in every string of a decoded document (keys
// are kept; they name settings, not values).
func redactValues(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = redactValues(e)
		}
	case []any:
		for i, e := range x {
			x[i] = redactValues(e)
		}
	case string:
		return dlog.Redact(x)
	}
	return v
}

// readConfig reads Root/etc/deyroute/config.yaml (DEY-C014 when missing).
func (g *Globals) readConfig() ([]byte, error) {
	data, err := os.ReadFile(g.configPath())
	if err != nil {
		return nil, deyerr.Wrap(deyerr.C014, err, deyerr.Params{"path": config.DefaultPath}).
			WithFix(i18n.T(i18n.CLINotSetUpFix))
	}
	return data, nil
}

// configApply runs Local.ConfigApply with progress and prints the result;
// extra fields go into the --json document.
func (g *Globals) configApply(ctx context.Context, l api.Local, extra map[string]any) error {
	p := g.newProgress()
	cctx, cancel := longCtx(ctx)
	defer cancel()
	res, err := l.ConfigApply(cctx, p.step)
	if err != nil {
		return err
	}
	if g.JSON {
		doc := map[string]any{"apply": res, "steps": p.steps}
		for k, v := range extra {
			doc[k] = v
		}
		return g.emitJSON(doc)
	}
	g.say(i18n.CLIConfigApplied, orDash(strings.Join(res.Changed, ", ")))
	if res.Backup != "" {
		g.say(i18n.CLIConfigBackup, res.Backup)
	}
	for _, w := range res.Warnings {
		g.println(g.text("  " + g.sym().warn + " " + clean(w)))
	}
	return nil
}

// configEdit is `deyroute config edit` (spec section 14): the owner edits a
// private copy in $EDITOR; the result is validated (strict schema, every
// DEY-C rule, registered transports, immutable ids). When it is invalid
// the editor opens again with the DEY errors as comment lines on top, until
// the file is valid or emptied (abort). A valid file atomically replaces
// config.yaml and the daemon applies it.
func (g *Globals) configEdit(ctx context.Context) error {
	orig, err := g.readConfig()
	if err != nil {
		return err
	}
	opts := validateOptions()
	prev, perr := config.ParseWith(orig, opts)
	if perr != nil {
		prev = nil // the current file is broken: the edit repairs it
	}
	tmp, err := os.CreateTemp("", "deyroute-config-*.yaml")
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": os.TempDir()})
	}
	tmpPath := tmp.Name()
	keepCopy := false
	defer func() {
		if !keepCopy {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": tmpPath})
	}
	content := orig
	if perr != nil {
		content = append(editHeader(perr), orig...)
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": tmpPath})
	}
	if err := tmp.Close(); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": tmpPath})
	}
	var body, lastBad []byte
	var next *config.Config
	for round := 0; ; round++ {
		ectx, cancel := context.WithTimeout(ctx, EditorTimeout)
		err := g.Editor(ectx, tmpPath)
		cancel()
		if err != nil {
			return err
		}
		edited, err := os.ReadFile(tmpPath) // #nosec G304 -- our own temporary file
		if err != nil {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": tmpPath})
		}
		body = stripEditHeader(edited)
		if emptyYAML(body) {
			return g.editNotSaved("aborted", i18n.CLIConfigEditAborted)
		}
		if bytes.Equal(body, orig) {
			return g.editNotSaved("unchanged", i18n.CLIConfigUnchanged)
		}
		var verr error
		next, verr = config.ParseWith(body, opts)
		if verr == nil && prev != nil {
			verr = config.CheckImmutable(prev, next)
		}
		if verr == nil {
			break
		}
		if bytes.Equal(body, lastBad) || round+1 >= maxEditRounds {
			// The editor saved the same invalid file again (or the
			// owner gave up): stop instead of looping.
			return verr
		}
		lastBad = body
		fixed := append(editHeader(verr), body...)
		if err := os.WriteFile(tmpPath, fixed, 0o600); err != nil {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": tmpPath})
		}
		g.note(i18n.CLIConfigEditInvalid, len(deyErrors(verr)))
	}
	// config.yaml must still be the file the editor started from: another
	// command or the menu may have saved it meanwhile (tunnel add, node
	// rename …), and replacing it would silently undo that change. The
	// edited copy is kept for the owner.
	if cur, err := os.ReadFile(g.configPath()); err != nil || !bytes.Equal(cur, orig) {
		_ = os.WriteFile(tmpPath, body, 0o600)
		keepCopy = true
		return deyerr.New(deyerr.C024, deyerr.Params{"path": config.DefaultPath, "copy": tmpPath})
	}
	if err := config.WriteFileAtomic(g.configPath(), body, config.FileMode); err != nil {
		return deyerr.Wrap(deyerr.C017, err, deyerr.Params{"path": config.DefaultPath})
	}
	if !g.JSON {
		g.say(i18n.CLIConfigSaved, config.DefaultPath)
	}
	if next.Role == config.RoleNode {
		// The node agent has no apply (DEY-X009): it reads node.hub_addr
		// on every reconnect and everything else when it starts.
		if g.JSON {
			return g.emitJSON(map[string]any{"path": config.DefaultPath, "saved": true, "restart": node.ServiceName})
		}
		g.say(i18n.CLIConfigSavedNode, node.ServiceName)
		return nil
	}
	l, err := g.local()
	if err != nil {
		if !g.JSON {
			g.say(i18n.CLIConfigNotApplied, g.service())
		}
		return err
	}
	return g.configApply(ctx, l, map[string]any{"path": config.DefaultPath, "saved": true})
}

// editNotSaved reports an edit that changed nothing.
func (g *Globals) editNotSaved(reason string, k i18n.Key) error {
	if g.JSON {
		return g.emitJSON(map[string]any{"path": config.DefaultPath, "saved": false, "reason": reason})
	}
	g.say(k)
	return nil
}

// editHeader renders the validation errors as comment lines for the top
// of the edited file.
func editHeader(err error) []byte {
	var b strings.Builder
	b.WriteString(editHeaderPrefix + i18n.T(i18n.CLIConfigEditHeader) + "\n")
	b.WriteString(editHeaderPrefix + i18n.T(i18n.CLIConfigEditHeader2) + "\n")
	for _, e := range deyErrors(err) {
		for _, l := range strings.Split(strings.TrimRight(e.Format(true), "\n"), "\n") {
			b.WriteString(editHeaderPrefix + l + "\n")
		}
	}
	return []byte(b.String())
}

// stripEditHeader removes the lines editHeader added (wherever the owner
// left them).
func stripEditHeader(data []byte) []byte {
	lines := bytes.SplitAfter(data, []byte("\n"))
	out := make([]byte, 0, len(data))
	for _, l := range lines {
		if bytes.HasPrefix(l, []byte(editHeaderPrefix)) || bytes.Equal(bytes.TrimRight(l, "\r\n"), []byte(strings.TrimSpace(editHeaderPrefix))) {
			continue
		}
		out = append(out, l...)
	}
	return out
}

// emptyYAML reports a file without content: only blank and comment lines.
func emptyYAML(data []byte) bool {
	for _, l := range bytes.Split(data, []byte("\n")) {
		t := bytes.TrimSpace(l)
		if len(t) > 0 && t[0] != '#' {
			return false
		}
	}
	return true
}
