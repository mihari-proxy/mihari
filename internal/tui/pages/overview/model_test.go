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
		Monitor:    ui.MonitorSnapshot{Traffic: []ui.TrafficPoint{{Up: 1, Down: 2}}, MemoryInUse: 28_356 * 1024},
		Operations: []ui.OperationRecord{{Object: "mihomo", State: "Succeeded", At: time.Unix(100, 0).UTC()}},
	})
	view := model.View()
	for _, want := range []string{"running", "v1.19.0", "PID 42", "Desired 5", "Observed 4", "Web GUI", "Phase 5", "mihomo", "Succeeded", "27.7 MiB"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view does not contain %q: %s", want, view)
		}
	}
}

func TestOverview_EmptyConfigAndSubscriptionUseClearLabels(t *testing.T) {
	model := New()
	model.SetSize(90, 26)
	model.SetSnapshot(Snapshot{
		Core: protocol.CoreStatus{Status: "running", Version: "v1.19.0", PID: 1},
	})
	view := model.View()
	for _, want := range []string{ui.ConfigNotAppliedLabel, ui.NoSubscriptionsConfiguredLabel, ui.WebGUIPhaseBoundary} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q: %s", want, view)
		}
	}
	if strings.Contains(view, ui.UnavailableTitle) {
		t.Fatalf("empty overview should not use bare Unavailable: %s", view)
	}
}

func TestOverview_NoActiveSubscriptionWhenListHasProfiles(t *testing.T) {
	model := New()
	model.SetSize(90, 26)
	model.SetSnapshot(Snapshot{
		Subscriptions: protocol.SubscriptionList{
			Subscriptions: []protocol.Subscription{{ID: "one", Name: "Main"}},
		},
	})
	view := model.View()
	if !strings.Contains(view, ui.NoActiveSubscriptionLabel) {
		t.Fatalf("view=%s", view)
	}
}
