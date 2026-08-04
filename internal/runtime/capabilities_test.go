package runtime

import (
	"context"
	"slices"
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

type stubGateway struct{}

func (stubGateway) Serve(context.Context) error { return nil }
func (stubGateway) SessionCount() int           { return 0 }
func (stubGateway) ListenAddr() string          { return "127.0.0.1:9191" }

func TestCapabilitiesIncludeWebGUIWhenGatewayAndPanelsPresent(t *testing.T) {
	manager := newTestManager(Options{
		Panels:     &fakePanels{},
		WebGateway: stubGateway{},
	})
	if !slices.Contains(manager.Capabilities(), protocol.CapabilityWebGUI) {
		t.Fatalf("capabilities=%v", manager.Capabilities())
	}
}

func TestCapabilitiesOmitWebGUIWithoutGateway(t *testing.T) {
	manager := newTestManager(Options{Panels: &fakePanels{}})
	if slices.Contains(manager.Capabilities(), protocol.CapabilityWebGUI) {
		t.Fatalf("capabilities=%v", manager.Capabilities())
	}
}
