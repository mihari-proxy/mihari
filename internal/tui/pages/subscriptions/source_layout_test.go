package subscriptions

import (
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestLocalSource_FieldFitsNarrowForm(t *testing.T) {
	f := newAddForm()
	f.inputs[3].SetValue("Local file")
	layout := f.fieldLayout(ui.DefaultTheme(), 18)
	field := layout.fields[3]
	for _, line := range layout.lines[field.first : field.last+1] {
		if lipgloss.Width(line) > 18 {
			t.Fatalf("source exceeds form width: %q (%d)", line, lipgloss.Width(line))
		}
	}
}
