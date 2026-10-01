package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
)

// handle is the api.CommandHandler of the control client: it executes one
// hub command. Errors are DEY errors whose cause text is copied into Detail
// (the wire form carries no cause) and are remembered for the heartbeat.
func (a *agent) handle(ctx context.Context, cmd api.Command, stream func(lines []string)) (any, error) {
	a.touch()
	a.log.Debug("command from hub", slog.String("command", cmd.Name), slog.String("command_id", cmd.ID))
	data, err := a.dispatch(ctx, cmd, stream)
	if err == nil {
		return data, nil
	}
	if ctx.Err() != nil {
		// The hub cancelled the command or its deadline passed: the control
		// client reports DEY-N014/N005 for a plain error.
		var de *deyerr.Error
		if !errors.As(err, &de) || de.Code == deyerr.X031 || de.Code == deyerr.X000 {
			return nil, ctx.Err()
		}
		return nil, err
	}
	var de *deyerr.Error
	if !errors.As(err, &de) {
		// No DEY code: the control client sends the redacted text (N011).
		a.setLastError(err)
		a.log.Warn("command failed", slog.String("command", cmd.Name), dlog.Err(err))
		return nil, err
	}
	if de.Detail == "" && de.Cause != nil {
		de.Detail = dlog.Redact(de.Cause.Error())
	}
	a.setLastError(de)
	a.log.Warn("command failed", slog.String("command", cmd.Name), dlog.Code(de.Code), dlog.Err(err))
	return nil, err
}

// decodeArgs unmarshals cmd.Args into v; commands that need arguments
// refuse an empty payload.
func (a *agent) decodeArgs(cmd api.Command, v any) error {
	raw := bytes.TrimSpace(cmd.Args)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return a.refuse(cmd.Name, "the command has no arguments")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return deyerr.Wrap(deyerr.N015, err, deyerr.Params{"node": a.nodeID, "reason": "invalid arguments for " + cmd.Name})
	}
	return nil
}

// dispatch runs one command by name.
func (a *agent) dispatch(ctx context.Context, cmd api.Command, stream func([]string)) (any, error) {
	switch cmd.Name {
	case api.CmdBackendInstall:
		var args api.BackendInstallArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return a.backendInstall(ctx, args)
	case api.CmdBackendRender:
		var args api.BackendRenderArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return nil, a.backendRender(ctx, args)
	case api.CmdBackendRemove:
		var args api.BackendRemoveArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return nil, a.backendRemove(ctx, args)
	case api.CmdUnitStart, api.CmdUnitRestart, api.CmdUnitStop, api.CmdUnitStatus:
		var args api.UnitArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		switch cmd.Name {
		case api.CmdUnitStart:
			return a.unitStart(ctx, args, false)
		case api.CmdUnitRestart:
			return a.unitStart(ctx, args, true)
		case api.CmdUnitStop:
			return a.unitStop(ctx, args)
		}
		return a.unitStatusCmd(ctx, args)
	case api.CmdProbeTCP, api.CmdProbeTLS, api.CmdProbeHTTP:
		var args api.ProbeArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return a.probe(ctx, cmd.Name, args)
	case api.CmdProbeUDPListen:
		var args api.UDPListenArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return nil, a.udpListen(ctx, args)
	case api.CmdPortCheckRemote:
		var args api.PortCheckArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return a.portCheck(ctx, args)
	case api.CmdFetchProxy:
		var args api.FetchArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return a.fetchProxy(ctx, args)
	case api.CmdSysinfo:
		return a.sysinfo(), nil
	case api.CmdMetrics:
		var args api.MetricsArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return a.metrics(ctx, args), nil
	case api.CmdLogsTail:
		var args api.LogsArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return a.logsTail(ctx, args, stream)
	case api.CmdDoctorCollect:
		return a.doctorData(ctx), nil
	case api.CmdSelfUpdate:
		var args api.SelfUpdateArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return nil, a.selfUpdate(ctx, args)
	case api.CmdSetHub:
		var args api.SetHubArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return nil, a.setHub(ctx, args.Addr, a.o.ReconnectDelay)
	case api.CmdUninstall:
		a.scheduleUninstall()
		return nil, nil
	case api.CmdCertRenew:
		var args api.CertRenewArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return a.certRenew(ctx, args)
	case api.CmdCertInstall:
		var args api.CertInstallArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return nil, a.certInstall(ctx, args)
	case api.CmdHTTPPost:
		var args api.HTTPPostArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return a.httpPost(ctx, args)
	case api.CmdEchoStart:
		var args api.EchoArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return a.echoStart(ctx, args)
	case api.CmdEchoStop:
		var args api.EchoArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return nil, a.echoStop(args)
	case api.CmdNodeFirewall:
		var args api.NodeFirewallArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return nil, a.nodeFirewall(ctx, args)
	case api.CmdSysctlApply:
		var args api.SysctlArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return nil, a.sysctlApply(args)
	case api.CmdSpeedServe:
		var args api.SpeedServeArgs
		if err := a.decodeArgs(cmd, &args); err != nil {
			return nil, err
		}
		return nil, a.speedServe(ctx, args)
	}
	return nil, deyerr.New(deyerr.N015, deyerr.Params{"node": a.nodeID, "reason": "unknown command " + cmd.Name})
}
