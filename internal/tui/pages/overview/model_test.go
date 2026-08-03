package overview

import (
	"strings"
	"testing"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestOverview_RendersAuthoritativeCardsAndSessionOperations(t *testing.T) {
	model := New()
	model.SetSize(90, 26)
	model.SetSnapshot(Snapshot{
		Status: protocol.Status{
			Health: "ok", Revision: 7,
			Config: &protocol.ConfigStatus{Status: "applying", DesiredRevision: 5, ObservedRevision: 4},
		},
		Core:       protocol.CoreStatus{Status: "running", Version: "v1.19.0", PID: 42},
		Monitor:    ui.MonitorSnapshot{Traffic: []ui.TrafficPoint{{Up: 1, Down: 2}}},
		Operations: []ui.OperationRecord{{Object: "mihomo", State: "Succeeded", At: time.Unix(100, 0).UTC()}},
	})
	view := model.View()
	for _, want := range []string{"running", "v1.19.0", "PID 42", "Desired 5", "Observed 4", "Web GUI", "Unavailable", "mihomo", "Succeeded", "█"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view does not contain %q: %s", want, view)
		}
	}
}
