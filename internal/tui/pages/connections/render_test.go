package connections

import (
	"strings"
	"testing"

	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestView_DualSectionFraming(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(80, 24)
	view := model.View()
	if !strings.Contains(view, "\u256d") || !strings.Contains(view, "\u2570") {
		t.Fatalf("missing section borders:\n%s", view)
	}
	if !strings.Contains(view, ui.ControlsSectionTitle) {
		t.Fatalf("missing Controls title:\n%s", view)
	}
	if !strings.Contains(view, "Connections") {
		t.Fatalf("missing Connections list title:\n%s", view)
	}
	if !strings.Contains(view, ui.SearchPlaceholder) && !strings.Contains(view, "Search") {
		t.Fatalf("search should still appear:\n%s", view)
	}
	// Active mode default title.
	want := ui.FormatConnectionsTitle(true, 0)
	if !strings.Contains(view, want) {
		t.Fatalf("missing list title %q in:\n%s", want, view)
	}
}

func TestView_ClosedDatasetTitle(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(80, 24)
	model.dataset = datasetClosed
	view := model.View()
	want := ui.FormatConnectionsTitle(false, 0)
	if !strings.Contains(view, want) {
		t.Fatalf("missing closed list title %q in:\n%s", want, view)
	}
}
