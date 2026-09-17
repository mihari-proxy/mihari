package runtime

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/state"
	"testing"
)

type metadataInstaller struct {
	CoreInstaller
	channel string
}

func (i *metadataInstaller) LatestVersion(_ context.Context, channel string) (string, error) {
	i.channel = channel
	return "alpha-abc1234", nil
}

type metadataPanels struct {
	PanelService
	id string
}

func (p *metadataPanels) LatestBuild(_ context.Context, id string) (string, error) {
	p.id = id
	return "v2.0.0", nil
}
func TestVersionChecks_DoNotRequireMutationGateOrChangeRevision(t *testing.T) {
	installer := &metadataInstaller{}
	panels := &metadataPanels{}
	// An uninitialized mutation gate would block if either check tried to acquire it.
	m := &Manager{installer: installer, panels: panels, settings: config.Settings{CoreChannel: "alpha"}, store: state.NewStore(state.Snapshot{Revision: 8})}
	result, err := m.CheckCoreVersion(t.Context())
	if err != nil || result.Channel != "alpha" || installer.channel != "alpha" || result.Latest != "alpha-abc1234" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	result, err = m.CheckPanelVersion(t.Context(), "zashboard")
	if err != nil || result.Latest != "v2.0.0" || panels.id != "zashboard" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if m.store.Load().Revision != 8 {
		t.Fatal("version check mutated revision")
	}
}
