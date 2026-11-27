package system

import (
	"fmt"
	"reflect"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func (m *Model) overrideHint() string {
	o := &m.overrides
	if o.category == "" {
		return "↑/↓ select · Enter open · Esc back"
	}
	toggle := "Ctrl+O Full preview"
	if o.full {
		toggle = "Ctrl+O Focused preview"
	}
	if o.reveal {
		toggle += " · Ctrl+R hide passwords"
	} else {
		toggle += " · Ctrl+R reveal"
	}
	if o.edit != "" {
		return "Tab accept · Ctrl+S save · Esc cancel · " + toggle
	}
	if o.previewFocus {
		return "↑/↓ scroll · PgUp/PgDn page · Tab edit · " + toggle
	}
	if o.category == "dns" {
		return "←/→ choose · Enter edit · Tab preview · Ctrl+S save · " + toggle
	}
	return "a add · i import · Enter edit · r rename · Space toggle · K/J move · d delete · Tab preview · Ctrl+S save · " + toggle
}

func (m *Model) overridePaneDimensions() (int, int) {
	w := max(10, m.layoutWidth()-4)
	h := max(4, m.height-8)
	if w >= 84 {
		return (w - 3) / 2, h
	}
	return w, max(2, (h-1)/2)
}

func (m *Model) overrideView() string {
	view := m.overridePageContent()
	if m.height <= 0 {
		return view
	}
	return strings.Join(ui.SliceLines(strings.Split(view, "\n"), 0, m.height), "\n")
}
func (m *Model) overridePageContent() string {
	o := &m.overrides
	if o.category == "" {
		lines := []string{m.theme.Title.Render("Configuration Overrides"), ""}
		for i, label := range overrideEditors {
			if i == o.menu {
				label = m.theme.RowSelected.Render("▸ " + label)
			} else {
				label = "  " + label
			}
			lines = append(lines, label)
		}
		return strings.Join(lines, "\n")
	}
	title := overrideEditors[o.menu]
	if !o.loaded {
		return m.theme.Title.Render(title) + "\n" + diagnostics.EscapeTerminal(o.notice) + "\nEsc back"
	}
	width, height := m.overridePaneDimensions()
	left := m.overrideControls(width, height)
	right := m.overridePreviewView(width, height)
	body := left + "\n" + m.theme.Muted.Render(strings.Repeat("─", width)) + "\n" + right
	if m.layoutWidth()-4 >= 84 {
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, " │ ", right)
	}
	state := o.notice
	if state == "" && (!reflect.DeepEqual(o.draft, o.status.Config) || o.edit != "") {
		state = "Unsaved changes"
	}
	if state == "" {
		state = "Saved"
	}
	if o.saving {
		state = "Working…"
	}
	if o.confirmDiscard {
		state = "Discard changes? Enter discard · Esc keep editing"
	}
	return strings.Join([]string{m.theme.Title.Render("Configuration Overrides · " + title), ui.TruncateVisible(diagnostics.EscapeTerminal(state), max(10, m.layoutWidth()-4)), body, m.theme.Muted.Render(ansi.Wrap(m.overrideHint(), max(10, m.layoutWidth()-4), ""))}, "\n")
}

func (m *Model) overrideControls(width, height int) string {
	o := &m.overrides
	if o.edit != "" {
		input := o.input.View()
		if o.edit == "YAML" {
			input = strings.ReplaceAll(input, yamlReplacementCell, "�")
		}
		return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(o.edit + "\n" + input)
	}
	var rows []string
	if o.category == "dns" {
		for _, key := range dnsFields {
			v := o.draft.DNS[key]
			label := dnsChoice(v)
			if len(v.Servers) > 0 {
				label += " " + strings.Join(v.Servers, ", ")
			}
			rows = append(rows, key+"  "+diagnostics.EscapeTerminal(label))
		}
	} else {
		for i, f := range o.draft.Fragments {
			state := "off"
			if f.Enabled {
				state = "on"
			}
			rows = append(rows, fmt.Sprintf("%d. [%s] %s", i+1, state, diagnostics.EscapeTerminal(f.Name)))
		}
		if len(rows) == 0 {
			rows = append(rows, "No YAML fragments · a add · i import")
		}
	}
	for i, row := range rows {
		prefix := "  "
		if i == o.selected && !o.previewFocus {
			prefix = "▸ "
		}
		rows[i] = ui.TruncateVisible(prefix+row, width)
		if prefix == "▸ " {
			rows[i] = m.theme.RowSelected.Render(rows[i])
		}
	}
	start := max(0, min(max(0, len(rows)-height), o.selected-height+1))
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(strings.Join(ui.SliceLines(rows, start, height), "\n"))
}

// overridePreviewDisplay caches wrapped lines for one draft and terminal width.
type overridePreviewDisplay struct {
	width           int
	rows            []overrideWrappedRow
	fields, sources map[string]int
}

type overrideWrappedRow struct {
	text string
	line int
}

func formatOverridePreview(lines []protocol.OverridePreviewLine, width int) overridePreviewDisplay {
	d := overridePreviewDisplay{width: width, fields: make(map[string]int), sources: make(map[string]int)}
	for i, line := range lines {
		if _, ok := d.fields[line.Field]; !ok {
			d.fields[line.Field] = len(d.rows)
		}
		if _, ok := d.sources[line.Source]; !ok {
			d.sources[line.Source] = len(d.rows)
		}
		mark := "  "
		switch line.Change {
		case "added", "+":
			mark = "+ "
		case "removed", "-":
			mark = "- "
		}
		text := mark + diagnostics.EscapeTerminal(line.Text)
		if line.Source != "" {
			text += "  # " + diagnostics.EscapeTerminal(line.Source)
		}
		for _, part := range strings.Split(ansi.Hardwrap(text, width, true), "\n") {
			d.rows = append(d.rows, overrideWrappedRow{text: part, line: i})
		}
	}
	return d
}
func (m *Model) overridePreviewView(width, height int) string {
	o := &m.overrides
	title := "Focused preview"
	if o.full {
		title = "Full preview"
	}
	name := o.status.ActiveName
	if name == "" {
		name = o.status.ActiveID
	}
	if name != "" {
		title += " · " + diagnostics.EscapeTerminal(name)
	} else {
		title += " · No active subscription"
	}
	if o.display.width != width {
		o.display = formatOverridePreview(o.preview, width)
	}
	visible := []string{m.theme.Title.Render(ui.TruncateVisible(title, width))}
	if o.previewPending {
		visible = append(visible, "Updating · previous preview is stale")
	}
	if o.previewError != "" {
		visible = append(visible, strings.Split(ansi.Wrap("Preview stale: "+diagnostics.EscapeTerminal(o.previewError), width, ""), "\n")...)
	}
	budget := max(1, height-len(visible))
	if !o.previewFocus {
		var focus int
		var found bool
		if o.category == "dns" {
			focus, found = o.display.fields["dns."+dnsFields[o.selected]]
		} else if len(o.draft.Fragments) > o.selected {
			focus, found = o.display.sources[o.draft.Fragments[o.selected].Name]
		}
		if found {
			o.top = ui.EnsureLineVisible(o.top, budget, len(o.display.rows), focus, focus+1)
		}
	}
	o.top = min(o.top, max(0, len(o.display.rows)-budget))
	for _, row := range o.display.rows[o.top:min(len(o.display.rows), o.top+budget)] {
		line := o.preview[row.line]
		style := lipgloss.NewStyle()
		switch line.Change {
		case "added", "+":
			style = m.theme.Success
		case "removed", "-":
			style = m.theme.Danger
		}
		selected := false
		if o.category == "dns" {
			field := "dns." + dnsFields[o.selected]
			selected = line.Field == field || strings.HasPrefix(line.Field, field+"[")
		} else if o.selected < len(o.draft.Fragments) {
			selected = line.Source == o.draft.Fragments[o.selected].Name
		}
		if selected {
			style = m.theme.PreviewFocus.Inherit(style).Width(width)
		}
		visible = append(visible, style.Render(row.text))
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(strings.Join(visible, "\n"))
}
