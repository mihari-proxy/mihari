package connections

import (
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
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

func TestView_PositionIndicator(t *testing.T) {
	// Empty history, focus on control → "0/0".
	empty := New(nil, nil)
	empty.SetSize(80, 24)
	if view := empty.View(); !strings.Contains(view, "0/0") {
		t.Fatalf("empty list should show 0/0:\n%s", view)
	}

	// Two connections; focus on the control strip (not a data row) → "—/2".
	controlled := New(nil, nil)
	controlled.SetSize(80, 24)
	controlled.Observe(protocol.ConnectionList{Connections: []protocol.Connection{
		{ID: "one", Metadata: protocol.ConnectionMetadata{Host: "one.test"}},
		{ID: "two", Metadata: protocol.ConnectionMetadata{Host: "two.test"}},
	}}, time.Unix(1, 0))
	controlled.focus = pageFocus{kind: focusControl}
	if view := controlled.View(); !strings.Contains(view, "—/2") {
		t.Fatalf("control focus should show —/2:\n%s", view)
	}

	// Focus on the first connection row → "1/2".
	onRow := New(nil, nil)
	onRow.SetSize(80, 24)
	onRow.Observe(protocol.ConnectionList{Connections: []protocol.Connection{
		{ID: "one", Metadata: protocol.ConnectionMetadata{Host: "one.test"}},
		{ID: "two", Metadata: protocol.ConnectionMetadata{Host: "two.test"}},
	}}, time.Unix(1, 0))
	onRow.focus = pageFocus{kind: focusRow, rowID: "one"}
	if view := onRow.View(); !strings.Contains(view, "1/2") {
		t.Fatalf("focused first row should show 1/2:\n%s", view)
	}
}
