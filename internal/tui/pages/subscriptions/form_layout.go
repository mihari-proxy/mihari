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
		if label == "Enabled" {
			layout.lines = append(layout.lines, "")
			layout.lines = append(layout.lines, strings.Split(ansi.Wrap(theme.Title.Render("Actions · Apply immediately"), width, ""), "\n")...)
		}
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
		suffix := ""
		if label == "Interval" {
			const units = "ns/us/ms/s/m/h"
			if available < len(units)+6 {
				layout.lines = append(layout.lines, theme.Muted.Render(prefix))
				prefix = "  "
				available = width - len(prefix)
			}
			suffix = " " + theme.Muted.Render(units)
			available = max(2, available-lipgloss.Width(suffix))
		}
		// Bubbles reserves an additional cell for its cursor, including when blurred.
		f.inputs[index].SetWidth(available - 1)
		value := f.inputs[index].View() + suffix
		action := label == "Enabled" || label == "InUse"
		if action {
			value = f.inputs[index].Value()
		}
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
		if action && lipgloss.Width(prefix+value) > width {
			layout.lines = append(layout.lines, theme.Muted.Render(prefix))
			prefix = ""
		}
		line := theme.Muted.Render(prefix) + value
		if (cycle || action) && index == f.index {
			line = theme.Muted.Render(prefix) + theme.RowFocus.Render(value)
		}
		layout.lines = append(layout.lines, line)
		if action && index == f.index {
			help := "Use requires an enabled subscription with a valid cache."
			if label == "Enabled" {
				help = "Disabling also clears InUse. Enabling does not select the subscription."
			}
			layout.lines = append(layout.lines, strings.Split(ansi.Wrap(theme.Muted.Render(help), width, ""), "\n")...)
		}
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
		layout.fields = append(layout.fields, formFieldRows{first, len(layout.lines) - 1})
	}
	return layout
}
