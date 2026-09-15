package subscriptions

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// formTextWidth reserves the dialog frame within the shared width cap.
func (m *Model) formTextWidth() int {
	return min(72, max(24, m.layoutWidth()-8)) - m.theme.Dialog.GetHorizontalFrameSize()
}

// formLayout combines status and field rows within the available body height.
// Global interval text is a placeholder and never becomes a draft value.
func (m *Model) formLayout() formLayout {
	width := m.formTextWidth()
	if m.form.kind == formEdit {
		global := m.globalInterval
		if global == "" {
			global = ui.MissingValue
		}
		m.form.inputs[2].Placeholder = "Global · " + global
	}
	layout := m.form.fieldLayout(m.theme, width)
	if m.form.kind == formEdit {
		status := strings.TrimSuffix(m.formStatus(), "\n")
		prefix := strings.Split(ansi.Wrap(status, width, ""), "\n")
		prefix = append(prefix, "", m.theme.Title.Render("Settings"), "")
		for i := range layout.fields {
			layout.fields[i].first += len(prefix)
			layout.fields[i].last += len(prefix)
		}
		layout.lines = append(prefix, layout.lines...)
	}
	if m.form.errorText != "" {
		layout.lines = append(layout.lines, "")
		layout.lines = append(layout.lines, strings.Split(ansi.Wrap(m.theme.Danger.Render(m.form.errorText), width, ""), "\n")...)
	}
	height := m.height
	if height == 0 {
		height = 32
	}
	// Reserve outer breathing room, dialog frame, title + gap, separator + Save.
	budget := max(2, height-2-m.theme.Dialog.GetVerticalFrameSize()-4)
	layout.bodyHeight = min(len(layout.lines), budget)
	return layout
}

// ensureFormFocus reveals the active field using its rendered row bounds.
// Moving focus ends manual scrolling; Save stays outside the scrollable body.
func (m *Model) ensureFormFocus() {
	m.dialogManualScroll = false
	layout := m.formLayout()
	if m.form.index < len(layout.fields) {
		field := layout.fields[m.form.index]
		if field.first < m.dialogScroll {
			m.dialogScroll = field.first
		}
		if field.last >= m.dialogScroll+layout.bodyHeight {
			m.dialogScroll = min(field.first, field.last-layout.bodyHeight+1)
		}
	} else if m.form.errorText != "" {
		m.dialogScroll = len(layout.lines) - layout.bodyHeight
	}
	m.dialogScroll = max(0, min(m.dialogScroll, len(layout.lines)-layout.bodyHeight))
}

// formView centers a content-sized dialog with a scrollable body and fixed Save.
// Non-editing phases replace the form with their existing status controls.
func (m *Model) formView(title string) string {
	width := m.formTextWidth()
	var body string
	if m.saveState != saveEditing {
		body = ansi.Wrap(m.saveBody(), width, "")
	} else {
		layout := m.formLayout()
		m.dialogScroll = max(0, min(m.dialogScroll, len(layout.lines)-layout.bodyHeight))
		end := min(len(layout.lines), m.dialogScroll+layout.bodyHeight)
		visible := strings.Join(layout.lines[m.dialogScroll:end], "\n")
		save := "[ Save ]"
		if m.form.index == len(m.form.inputs) {
			save = m.theme.RowFocus.Render(ui.FocusMarker + save)
		}
		save = lipgloss.PlaceHorizontal(width, lipgloss.Center, save)
		rule := strings.Repeat("─", width)
		if m.dialogScroll > 0 || end < len(layout.lines) {
			indicator := " PgUp/PgDn "
			if m.dialogScroll > 0 {
				indicator = "↑" + indicator
			}
			if end < len(layout.lines) {
				indicator += "↓"
			}
			rule = indicator + strings.Repeat("─", max(0, width-lipgloss.Width(indicator)))
		}
		body = visible + "\n" + m.theme.Muted.Render(rule) + "\n" + save
	}
	content := m.theme.Title.Render(ui.TruncateVisible(title, width)) + "\n\n" + body
	box := m.theme.Dialog.Width(width + m.theme.Dialog.GetHorizontalFrameSize()).Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
