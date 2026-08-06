package logs

import (
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestView_DualSectionFraming(t *testing.T) {
	model := New(10)
	model.SetSize(80, 24)
	model.Append(logAt("hello from logs", "info", 1))
	view := model.View()
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╰") {
		t.Fatalf("missing section borders:\n%s", view)
	}
	if !strings.Contains(view, ui.ControlsSectionTitle) {
		t.Fatalf("missing Controls:\n%s", view)
	}
	if !strings.Contains(view, ui.LogsSectionTitle) {
		t.Fatalf("missing Logs title:\n%s", view)
	}
	if !strings.Contains(view, "hello from logs") {
		t.Fatalf("log body missing:\n%s", view)
	}
}
