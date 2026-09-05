package runtime

import (
	"context"
	"log/slog"
	"slices"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
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

func TestCapabilitiesIncludeLoggingWhenRuntimePresent(t *testing.T) {
	manager := newTestManager(Options{Logging: &recordingLoggingRuntime{
		cfg: logging.Config{Level: slog.LevelInfo, MaxSizeBytes: 10 << 20, MaxFiles: 3},
	}})
	if !slices.Contains(manager.Capabilities(), protocol.CapabilityLogging) {
		t.Fatalf("capabilities=%v", manager.Capabilities())
	}
}

func TestCapabilitiesOmitLoggingWhenRuntimeUnavailable(t *testing.T) {
	manager := newTestManager(Options{})
	if slices.Contains(manager.Capabilities(), protocol.CapabilityLogging) {
		t.Fatalf("capabilities=%v", manager.Capabilities())
	}
}
