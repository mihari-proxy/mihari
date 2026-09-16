package subscriptions

import (
	"fmt"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// formFieldRows records inclusive row bounds in the rendered body, not field indices.
type formFieldRows struct{ first, last int }

type formLayout struct {
	lines      []string
	fields     []formFieldRows
	bodyHeight int
}

// fieldLayout renders shared add/edit fields and records their inclusive row bounds.
// Display widths and clipping preserve the full input values and cursor offsets.
func (f *formModel) fieldLayout(theme ui.Theme, width int) formLayout {
	layout := formLayout{}
	for index, label := range f.labels {
		first := len(layout.lines)
		marker := "  "
		if index == f.index {
			marker = ui.FocusMarker
		}
		prefix := marker + fmt.Sprintf("%-14s", label)
		if label == "URL" {
			layout.lines = append(layout.lines, marker+theme.Muted.Render(label))
			prefix = "  "
		}
		available := max(2, width-lipgloss.Width(prefix))
		// Bubbles reserves an additional cell for its cursor, including when blurred.
		f.inputs[index].SetWidth(available - 1)
		value := f.inputs[index].View()
		if label == "URL" && index != f.index && f.inputs[index].Value() != "" {
			// Reading shows the origin; editing retains the full value and cursor offset.
			value = f.inputs[index].Styles().Blurred.Text.Render(ui.TruncateVisible(f.inputs[index].Value(), available))
		}
		cycle := label == "Mode" || label == "Auto refresh"
		if cycle {
			value = proxyModeLabel(f.inputs[index].Value())
			if label == "Auto refresh" {
				value = "Off"
				if f.inputs[index].Value() == "true" {
					value = "On"
				}
			}
			value = "‹ " + value + " ›"
		}
		if label == "Mode" && lipgloss.Width(prefix+value) > width {
			layout.lines = append(layout.lines, theme.Muted.Render(prefix))
			prefix = "  "
		}
		line := theme.Muted.Render(prefix) + value
		if cycle && index == f.index {
			line = theme.RowFocus.Render(ui.PadCell(prefix+value, width, ui.AlignLeft))
		}
		layout.lines = append(layout.lines, line)
		if label == "Mode" && index == f.index {
			help := "Download subscription YAML directly."
			switch f.inputs[index].Value() {
			case "proxy":
				help = "Download subscription YAML via proxy."
			case "auto":
				help = "Download subscription YAML via proxy; retry DIRECT on eligible network errors."
			}
			layout.lines = append(layout.lines, strings.Split(ansi.Wrap(theme.Muted.Render(help), width, ""), "\n")...)
		}
		if label == "Interval" && index == f.index {
			help := "Leave blank to use global interval"
			layout.lines = append(layout.lines, strings.Split(ansi.Wrap(theme.Muted.Render(help), width, ""), "\n")...)
		}
		layout.fields = append(layout.fields, formFieldRows{first, len(layout.lines) - 1})
	}
	return layout
}
