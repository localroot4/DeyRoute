package hub

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
)

// Every fixed progress step has its title in internal/i18n (hub.step.<id>),
// so the TUI and the CLI never show a bare step id.
func TestStepTitlesComeFromI18n(t *testing.T) {
	ids := []string{
		stepValidate, stepBackup, stepApply, stepReconcile,
		stepInstallHub, stepInstallNode, stepRender, stepFirewall, stepStart, stepProbe,
		stepCheckPorts, stepEngine, stepRestart, stepStop, stepRemove, stepConfig, stepCleanup,
		stepNewCA, stepHubCert, stepTrustOnly, stepTunnelsCA,
		stepResolve, stepDownload, stepInstall, stepNodes, stepRestart2,
		stepSpeedServer, stepDiagRender, stepDiagStart, stepMeasure, stepDiagStop,
	}
	for _, id := range ids {
		k := i18n.Key("hub.step." + id)
		require.NotEqual(t, string(k), i18n.T(k), "step %q has no title in en.go", id)
		require.Equal(t, i18n.T(k), stepTitle(id))
	}
	require.Equal(t, "install backend on hub", stepTitle(stepInstallHub))
	require.Equal(t, "restart "+ServiceName, stepTitle(stepRestart2))
	require.Equal(t, "no-such-step", stepTitle("no-such-step"), "an unknown id is shown as is")

	var got []api.Step
	rep := &steps{progress: func(st api.Step) { got = append(got, st) }}
	rep.emit(api.Step{ID: stepFirewall, Status: api.StepOK})
	rep.emitTitled(api.Step{ID: stepBackupReady, Status: api.StepOK, Title: i18n.T(i18n.HubTitleBackupReady, "nl-1")})
	require.Equal(t, []string{"firewall", "backup nl-1 ready (warm)"}, []string{got[0].Title, got[1].Title})
}
