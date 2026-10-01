package cli

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
)

// ageMagic starts every age-encrypted file.
const ageMagic = "age-encryption.org/v1"

// Backup entry names (internal/install backup format).
const (
	backupManifestEntry = "manifest.json"
	backupConfigEntry   = "etc/deyroute/config.yaml"
	maxBackupConfig     = 4 << 20
)

func newBackupCmd(g *Globals) *cobra.Command {
	var out string
	var noEncrypt bool
	cmd := &cobra.Command{
		Use:     "backup",
		Short:   i18n.T(i18n.CLIBackupShort),
		Long:    i18n.T(i18n.CLIBackupLong),
		Example: i18n.T(i18n.CLIBackupExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), LocalOpTimeout)
			defer cancel()
			if out != "" {
				abs, err := filepath.Abs(out)
				if err != nil {
					return usageErr(err.Error())
				}
				out = abs
			}
			pass := ""
			if !noEncrypt {
				var err error
				if pass, err = g.newPassphrase(); err != nil {
					return err
				}
			}
			path, events, evErr, err := g.backupRun(ctx, out, pass, noEncrypt)
			if err != nil {
				return err
			}
			if g.JSON {
				doc := map[string]any{"path": path, "encrypted": !noEncrypt, "events": events}
				if evErr != nil {
					doc["events_error"] = api.ToDTO(evErr)
				}
				return g.emitJSON(doc)
			}
			g.say(i18n.CLIBackupWritten, path)
			switch {
			case evErr != nil && errDaemonDown(evErr):
				g.say(i18n.CLIBackupNoEvents)
			case evErr != nil:
				g.say(i18n.CLIBackupEventsFailed, deyerr.As(evErr).Code)
			}
			if noEncrypt {
				g.say(i18n.CLIBackupPlainWarn)
			} else {
				g.say(i18n.CLIBackupKeepPass)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", i18n.T(i18n.CLIFlagBackupOut))
	cmd.Flags().BoolVar(&noEncrypt, "no-encrypt", false, i18n.T(i18n.CLIFlagNoEncrypt))
	return cmd
}

// newPassphrase gets the backup passphrase: $DEYROUTE_BACKUP_PASSPHRASE, or
// typed twice without echo on a terminal. Without either it is DEY-S008.
func (g *Globals) newPassphrase() (string, error) {
	if p := g.Getenv(EnvBackupPassphrase); p != "" {
		dlog.RegisterSecret(p)
		return p, nil
	}
	if !g.IsTTY {
		return "", deyerr.New(deyerr.S008, nil).WithFix(i18n.T(i18n.CLIPassphraseFix, EnvBackupPassphrase))
	}
	for i := 0; i < 3; i++ {
		p1, err := g.ReadPassword(i18n.T(i18n.CLIAskPassphrase))
		if err != nil {
			return "", err
		}
		if p1 == "" {
			return "", deyerr.New(deyerr.S008, nil)
		}
		p2, err := g.ReadPassword(i18n.T(i18n.CLIAskPassphraseAgain))
		if err != nil {
			return "", err
		}
		if p1 == p2 {
			dlog.RegisterSecret(p1)
			return p1, nil
		}
		g.note(i18n.CLIPassphraseMismatch)
	}
	return "", errAborted
}

// passphrase gets the passphrase of an existing backup (asked once).
func (g *Globals) passphrase() (string, error) {
	if p := g.Getenv(EnvBackupPassphrase); p != "" {
		dlog.RegisterSecret(p)
		return p, nil
	}
	if !g.IsTTY {
		return "", deyerr.New(deyerr.S008, nil).WithFix(i18n.T(i18n.CLIPassphraseFix, EnvBackupPassphrase))
	}
	p, err := g.ReadPassword(i18n.T(i18n.CLIAskPassphrase))
	if err != nil {
		return "", err
	}
	dlog.RegisterSecret(p)
	return p, nil
}

// backupRun writes a backup (shared by the CLI and the TUI). The events are
// exported through the daemon when it runs (see backupEvents); events is
// their count, -1 when the history could not be exported, and evErr says
// why.
func (g *Globals) backupRun(ctx context.Context, out, pass string, noEncrypt bool) (path string, events int, evErr, err error) {
	r, events, evErr := g.backupEvents(ctx)
	path, err = g.Ops.Backup(ctx, setup.BackupOptions{
		Root: g.Root, Out: out, Passphrase: pass, NoEncrypt: noEncrypt, Events: r, Now: g.Now,
	})
	return path, events, evErr, err
}

// backupEvents exports the event history through the daemon (spec section
// 5: the backup holds /etc/deyroute plus the events). A node keeps no event
// history: 0 events, nothing to say. Otherwise -1 and the reason when the
// daemon is down (DEY-X003) or the export failed; the backup is still
// written, without history.
func (g *Globals) backupEvents(ctx context.Context) (io.Reader, int, error) {
	if g.role() == config.RoleNode {
		return nil, 0, nil
	}
	l, err := g.local()
	if err != nil {
		return nil, -1, err
	}
	cctx, cancel := callCtx(ctx)
	evs, err := l.Events(cctx, api.EventQuery{})
	cancel()
	switch {
	case deyerr.HasCode(err, deyerr.X009):
		return nil, 0, nil
	case err != nil:
		return nil, -1, err
	}
	r, err := setup.EventsReader(evs)
	if err != nil {
		return nil, -1, err
	}
	return r, len(evs), nil
}

// backupInfo is what restore shows before replacing anything.
type backupInfo struct {
	Role, HubName, NodeID, PublicIP, Version string
	ControlPort                              int
	Created                                  time.Time
	Encrypted                                bool
}

// isEncrypted reports whether the backup file is age-encrypted.
func isEncrypted(path string) (bool, error) {
	f, err := os.Open(path) // #nosec G304 -- the owner's backup file
	if err != nil {
		// A mistyped path is not "an invalid backup": say what failed.
		return false, deyerr.Wrap(deyerr.S005, err, deyerr.Params{"file": filepath.Base(path)}).WithWhy(clean(err.Error()))
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, len(ageMagic))
	n, _ := io.ReadFull(f, head)
	return string(head[:n]) == ageMagic, nil
}

// inspectBackup reads manifest.json and config.yaml of a backup without
// restoring it (wrong passphrase: DEY-S004; not a backup: DEY-S005).
func inspectBackup(path, pass string) (backupInfo, error) {
	file := filepath.Base(path)
	bad := func(err error) (backupInfo, error) {
		return backupInfo{}, deyerr.Wrap(deyerr.S005, err, deyerr.Params{"file": file})
	}
	f, err := os.Open(path) // #nosec G304 -- the owner's backup file
	if err != nil {
		return bad(err)
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReader(f)
	head, _ := br.Peek(len(ageMagic))
	info := backupInfo{Encrypted: string(head) == ageMagic}
	var src io.Reader = br
	if info.Encrypted {
		if pass == "" {
			return backupInfo{}, deyerr.New(deyerr.S004, deyerr.Params{"file": file})
		}
		id, err := age.NewScryptIdentity(pass)
		if err != nil {
			return backupInfo{}, deyerr.Wrap(deyerr.S004, err, deyerr.Params{"file": file})
		}
		if src, err = age.Decrypt(br, id); err != nil {
			return backupInfo{}, deyerr.Wrap(deyerr.S004, err, deyerr.Params{"file": file})
		}
	}
	gz, err := gzip.NewReader(src)
	if err != nil {
		return bad(err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var man backupManifest
	haveMan, haveCfg := false, false
	for !haveMan || !haveCfg {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return bad(err)
		}
		switch strings.TrimPrefix(h.Name, "./") {
		case backupManifestEntry:
			data, err := io.ReadAll(io.LimitReader(tr, maxBackupConfig))
			if err != nil || json.Unmarshal(data, &man) != nil {
				return bad(deyerr.Plain("manifest.json is damaged"))
			}
			haveMan = true
		case backupConfigEntry:
			data, err := io.ReadAll(io.LimitReader(tr, maxBackupConfig))
			if err != nil {
				return bad(err)
			}
			c, err := config.Decode(data)
			if err != nil {
				return backupInfo{}, err
			}
			info.Role = c.Role
			if c.Hub != nil {
				info.HubName, info.PublicIP, info.ControlPort = c.Hub.Name, c.Hub.PublicIP, c.Hub.ControlPort
			}
			if c.Node != nil {
				info.NodeID = c.Node.ID
			}
			haveCfg = true
		}
	}
	if !haveCfg {
		return bad(deyerr.Plain("config.yaml is missing"))
	}
	info.Version, info.Created = man.Version, man.Created
	return info, nil
}

// backupManifest mirrors the fields of install.BackupManifest that restore
// shows.
type backupManifest struct {
	Version string    `json:"version"`
	Created time.Time `json:"created"`
}

func newRestoreCmd(g *Globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "restore FILE",
		Short:   i18n.T(i18n.CLIRestoreShort),
		Long:    i18n.T(i18n.CLIRestoreLong),
		Example: i18n.T(i18n.CLIRestoreExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), LocalOpTimeout)
			defer cancel()
			path, err := filepath.Abs(args[0])
			if err != nil {
				return usageErr(err.Error())
			}
			enc, err := isEncrypted(path)
			if err != nil {
				return err
			}
			pass := ""
			if enc {
				if pass, err = g.passphrase(); err != nil {
					return err
				}
			}
			info, err := inspectBackup(path, pass)
			if err != nil {
				return err
			}
			newIP, err := g.restoreAddress(ctx, info, yes)
			if err != nil {
				return err
			}
			if err := g.confirm(g.restoreLost(path, info, newIP), yes); err != nil {
				return err
			}
			p := g.newProgress()
			res, err := g.restoreRun(ctx, path, pass, newIP, p.step)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{
					"role": res.Role, "hub_name": res.HubName, "node": res.NodeID, "public_ip": res.PublicIP,
					"address_changed": res.AddressChanged, "backup_version": res.BackupVersion, "created": res.Created,
					"previous_dir": res.PreviousDir, "migrated": res.Migrated, "sysctl_profile": res.SysctlProfile,
					"service_started": res.ServiceStarted, "steps": p.steps,
				})
			}
			g.println()
			if res.Role == config.RoleHub {
				g.say(i18n.CLIRestoredHub, res.HubName)
				addr := res.PublicIP
				if info.ControlPort > 0 {
					addr = res.PublicIP + ":" + strconv.Itoa(info.ControlPort)
				}
				g.say(i18n.CLIRestoreNextHub, addr, addr)
			} else {
				g.say(i18n.CLIRestoredNode, res.NodeID)
				g.say(i18n.CLIRestoreNextNode)
			}
			if res.PreviousDir != "" {
				g.say(i18n.CLIRestorePrevious, res.PreviousDir)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

// restoreAddress decides hub.public_ip for a restored hub: a hub moved to
// a new server takes this server's public address (spec section 5, "hub
// move"); the owner is asked on a terminal. "" keeps the backup's address:
// always when this server already is that hub (same-server restore), and
// when the detected address is not public.
func (g *Globals) restoreAddress(ctx context.Context, info backupInfo, yes bool) (string, error) {
	if info.Role != config.RoleHub || info.PublicIP == "" {
		return "", nil
	}
	if c := g.localConfig(); c != nil && c.Hub != nil && c.Hub.PublicIP == info.PublicIP {
		return "", nil
	}
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	ip, private, err := g.DetectIP(dctx)
	cancel()
	if err != nil || private || ip == "" || ip == info.PublicIP {
		return "", nil
	}
	if !g.IsTTY || yes {
		return ip, nil
	}
	use, err := g.askYesNo(i18n.T(i18n.CLIAskMovedHub, ip, info.PublicIP), true)
	if err != nil || !use {
		return "", err
	}
	return ip, nil
}

// restoreLost is the confirmation text of restore: what is replaced.
func (g *Globals) restoreLost(path string, info backupInfo, newIP string) string {
	who := info.Role + " " + info.HubName
	if info.Role == config.RoleNode {
		who = info.Role + " " + info.NodeID
	}
	created := localTime(info.Created, "2006-01-02 15:04")
	text := i18n.T(i18n.CLIRestoreLost, filepath.Base(path), strings.TrimSpace(who), created, orDash(info.Version))
	if newIP != "" {
		text += "\n" + i18n.T(i18n.CLIRestoreMove, info.PublicIP, newIP)
	}
	return text
}

// restoreSummary is what the menu shows after a restore: a changed hub
// address (the certificate was re-issued) and the next steps, named by
// their menu items.
func (g *Globals) restoreSummary(info backupInfo, res *setup.RestoreResult) string {
	var lines []string
	if res.Role != config.RoleHub {
		lines = append(lines, i18n.T(i18n.CLIRestoredNode, res.NodeID), i18n.T(i18n.CLIRestoreNextNode))
	} else {
		lines = append(lines, i18n.T(i18n.CLIRestoredHub, res.HubName))
		if res.AddressChanged {
			lines = append(lines, i18n.T(i18n.CLIRestoreMove, info.PublicIP, res.PublicIP))
		}
		addr := res.PublicIP
		if info.ControlPort > 0 {
			addr = res.PublicIP + ":" + strconv.Itoa(info.ControlPort)
		}
		lines = append(lines, i18n.T(i18n.CLIRestoreNextMenu, addr, addr, addr))
	}
	if res.PreviousDir != "" {
		lines = append(lines, i18n.T(i18n.CLIRestorePrevious, res.PreviousDir))
	}
	return strings.Join(lines, "\n")
}

// restoreRun restores a backup (shared by the CLI and the TUI).
func (g *Globals) restoreRun(ctx context.Context, path, pass, publicIP string, progress func(api.Step)) (*setup.RestoreResult, error) {
	return g.Ops.Restore(ctx, setup.RestoreOptions{
		Root: g.Root, Path: path, Passphrase: pass, Runner: g.Runner, PublicIP: publicIP,
		ApplySysctl: true, StartService: !g.NoService, SocketPath: g.Socket, Now: g.Now, Progress: progress,
		Logger: g.logger(), LookupUser: g.LookupUser, LookupGroup: g.LookupGroup, Chown: g.Chown,
	})
}
