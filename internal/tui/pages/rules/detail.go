package rules

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func (m *Model) detailLayout() (lines []string, inner, rows, height int) {
	height = m.height
	if height <= 0 {
		height = 28
	}
	inner = max(8, min(74, m.layoutWidth()-8))
	lines = strings.Split(ansi.Wrap(m.detail.body, ui.SectionTextWidth(inner), ""), "\n")
	rows = max(1, height-8)
	return
}

func (m *Model) updateDetail(key string) {
	lines, _, rows, _ := m.detailLayout()
	switch key {
	case "esc", "enter":
		m.detail = nil
		return
	case "up", "k":
		m.detail.scroll--
	case "down", "j":
		m.detail.scroll++
	case "pgup":
		m.detail.scroll -= rows
	case "pgdown":
		m.detail.scroll += rows
	case "home":
		m.detail.scroll = 0
	case "end":
		m.detail.scroll = len(lines)
	}
	m.detail.scroll = min(max(0, m.detail.scroll), max(0, len(lines)-rows))
}

func (m *Model) renderDetail(background string) string {
	lines, inner, rows, height := m.detailLayout()
	if height < 8 {
		dialog := m.theme.Warning.Render(ui.TruncateVisible("Resize terminal · Enter/Esc close", m.layoutWidth()))
		return ui.CenterOverlay(m.theme, background, dialog, m.layoutWidth(), height)
	}
	offset := min(max(0, m.detail.scroll), max(0, len(lines)-rows))
	footer := "Enter/Esc close"
	if len(lines) > rows {
		footer = fmt.Sprintf("↑/↓ PgUp/PgDn  %d/%d · %s", offset+1, len(lines), footer)
	}
	body := "\n" + strings.Join(ui.SliceLines(lines, offset, rows), "\n") + "\n\n" + m.theme.Muted.Render(ansi.Wrap(footer, ui.SectionTextWidth(inner), ""))
	dialog := ui.RenderBorderedSectionWithBorder(m.theme, m.detail.title, body, inner, m.theme.ColorAccent)
	return ui.CenterOverlay(m.theme, background, dialog, m.layoutWidth(), height)
}
