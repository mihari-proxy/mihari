package tui

import (
	"fmt"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type diagnosticLayout struct {
	boxWidth, innerWidth   int
	listWidth, detailWidth int
	listRows, detailRows   int
	stacked                bool
}

func (w *diagnosticWindow) widths(width int) (int, int) {
	inner := max(1, min(144, width-4)-4) // border plus one cell of padding per side
	if width < 88 {
		return inner, inner
	}
	list := min(48, max(28, inner*2/5))
	return list, max(1, inner-list-3)
}

func (w *diagnosticWindow) layout(width, height, detailLines int) diagnosticLayout {
	listWidth, detailWidth := w.widths(width)
	l := diagnosticLayout{boxWidth: max(1, min(144, width-4)), listWidth: listWidth, detailWidth: detailWidth, stacked: width < 88}
	l.innerWidth = max(1, l.boxWidth-4)
	// Outer margins, border, title and footer consume six terminal rows.
	bodyRows := max(4, height-6)
	if w.notice() != "" {
		bodyRows--
	}
	listLines := max(2, len(w.entries)*2)
	rows := max(1, min(bodyRows-1, max(listLines, detailLines)))
	l.listRows, l.detailRows = rows, rows
	if l.stacked {
		l.listRows = min(listLines, 6, max(2, (bodyRows-2)/3))
		l.listRows -= l.listRows % 2
		l.detailRows = max(1, min(detailLines, bodyRows-2-l.listRows))
	}
	return l
}

func (w *diagnosticWindow) notice() string {
	var parts []string
	for _, notice := range []string{w.copyStatus, w.remoteNotice, w.localNotice} {
		if notice != "" {
			parts = append(parts, notice)
		}
	}
	return strings.Join(parts, " · ")
}

func (w *diagnosticWindow) detailLines(width int) []string {
	_, detailWidth := w.widths(width)
	s := w.pinned
	var parts []string
	if w.selected == "" {
		parts = append(parts, "No diagnostic records")
	} else {
		// Lead with the cause so it remains visible even in a short terminal.
		// Show a distinct summary below the cause, without duplicating identical
		// error text. Full summaries remain readable even when the list clips them.
		body := s.Detail
		if body == "" {
			body = s.Summary
		}
		parts = append(parts, diagnostics.EscapeTerminal(body))
		if s.Detail != "" && s.Summary != "" && s.Summary != s.Detail {
			parts = append(parts, "", "Summary: "+diagnostics.EscapeTerminal(s.Summary))
		}
		if unavailable := diagnostics.AvailabilityText(s.State); unavailable != "" {
			parts = append(parts, unavailable)
		}
		if s.RetrievalError != "" {
			parts = append(parts, diagnostics.EscapeTerminal(s.RetrievalError))
		}
		if s.Truncated {
			parts = append(parts, "[Diagnostic capture truncated: "+diagnostics.EscapeTerminal(s.TruncationReason)+"]")
		}
		parts = append(parts, "")
		if !s.Time.IsZero() {
			parts = append(parts, "Time    "+s.Time.Local().Format("2006-01-02 15:04:05 -07:00"))
		}
		for _, field := range [][2]string{{"Source", s.Component}, {"Event", s.Event}, {"Code", string(s.Code)}, {"Action", s.Operation}, {"Object", s.Object}, {"Op ID", s.OperationID}} {
			if field[1] != "" {
				parts = append(parts, fmt.Sprintf("%-7s %s", field[0], diagnostics.EscapeTerminal(field[1])))
			}
		}
	}
	if w.historyQueryFailure != nil {
		parts = append(parts, "", diagnostics.TerminalText("History query", *w.historyQueryFailure))
	}
	text := strings.TrimRight(strings.Join(parts, "\n"), "\n")
	// Match Lip Gloss's tab expansion before measuring and wrapping lines.
	text = strings.ReplaceAll(text, "\t", "    ")
	// Word wrapping keeps ordinary sentences readable; Hardwrap also bounds
	// unbroken URLs and long tokens. Escaping happens before layout, never copy.
	text = ansi.Wrap(text, detailWidth, "")
	return strings.Split(ansi.Hardwrap(text, detailWidth, true), "\n")
}

func (w *diagnosticWindow) listView(theme ui.Theme, l diagnosticLayout) string {
	capacity := max(1, l.listRows/2)
	selected := w.selectedIndex()
	start := max(0, selected-capacity+1)
	var lines []string
	for i := start; i < len(w.entries) && i < start+capacity; i++ {
		s := w.entries[i].snapshot
		marker := "  "
		if s.ID == w.selected {
			marker = ui.FocusMarker
		}
		severity := diagnosticSingleLine(s.Severity)
		style := theme.Info
		switch strings.ToLower(s.Severity) {
		case "error":
			style = theme.Danger
		case "warning", "warn":
			style = theme.Warning
		}
		meta := marker + theme.Muted.Render(s.Time.Local().Format("15:04:05")) + " " + style.Render(severity) + " " + diagnosticSingleLine(s.Component)
		summary := "  " + diagnosticSingleLine(s.Summary)
		meta = ui.TruncateVisible(meta, l.listWidth)
		summary = ui.TruncateVisible(summary, l.listWidth)
		if s.ID == w.selected {
			// Nested severity/time resets would interrupt reverse video midway
			// through the focused row. Apply one style to its complete text.
			meta = ui.TruncateVisible(marker+s.Time.Local().Format("15:04:05")+" "+severity+" "+diagnosticSingleLine(s.Component), l.listWidth)
			rowStyle := theme.RowSelected
			if !w.detailFocus {
				rowStyle = theme.RowFocus
			}
			meta = rowStyle.Width(l.listWidth).Render(meta)
			summary = rowStyle.Width(l.listWidth).Render(summary)
		}
		lines = append(lines, meta, summary)
	}
	if len(lines) == 0 {
		lines = append(lines, theme.Muted.Render(ui.TruncateVisible("No diagnostic records", l.listWidth)))
	}
	return lipgloss.NewStyle().Width(l.listWidth).Height(l.listRows).MaxHeight(l.listRows).Render(strings.Join(lines, "\n"))
}

func diagnosticPaneTitle(theme ui.Theme, title string, focused bool, width int) string {
	style := theme.Muted
	if focused {
		style = theme.Title
		title = "▸ " + title
	} else {
		title = "  " + title
	}
	return style.Width(width).Render(ui.TruncateVisible(title, width))
}

func (w *diagnosticWindow) view(width, height int) string {
	theme := ui.DefaultTheme()
	if width < 30 || height < 12 {
		text := ansi.Hardwrap("Diagnostics\nEnlarge terminal\nEsc back", max(1, width), true)
		return lipgloss.NewStyle().MaxWidth(max(1, width)).MaxHeight(max(1, height)).Render(text)
	}
	lines := w.detailLines(width)
	l := w.layout(width, height, len(lines))
	start := min(w.scroll, max(0, len(lines)-l.detailRows))
	end := min(len(lines), start+l.detailRows)
	detail := lipgloss.NewStyle().Width(l.detailWidth).Height(l.detailRows).Render(strings.Join(lines[start:end], "\n"))
	position := fmt.Sprintf("%d/%d", max(0, w.selectedIndex()+1), len(w.entries))
	if w.selected != "" && w.selectedIndex() < 0 {
		position = "pinned"
	}
	listTitle := diagnosticPaneTitle(theme, "Records "+position, !w.detailFocus, l.listWidth)
	detailTitle := diagnosticPaneTitle(theme, fmt.Sprintf("Details %d–%d/%d", start+1, end, len(lines)), w.detailFocus, l.detailWidth)
	left, right := listTitle+"\n"+w.listView(theme, l), detailTitle+"\n"+detail
	body := left + "\n" + right
	if !l.stacked {
		divider := strings.TrimSuffix(strings.Repeat(theme.SurfaceBorder.Render(" │ ")+"\n", l.listRows+1), "\n")
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
	}
	header := theme.Title.Render("Diagnostics")
	count := theme.Muted.Render(fmt.Sprintf("%d records", len(w.entries)))
	header += strings.Repeat(" ", max(1, l.innerWidth-lipgloss.Width(header)-lipgloss.Width(count))) + count
	parts := []string{header, body}
	if notice := w.notice(); notice != "" {
		style := theme.Warning
		if notice == "Copied" {
			style = theme.Success
		}
		parts = append(parts, style.Render(ui.TruncateVisible(diagnosticSingleLine(notice), l.innerWidth)))
	}
	parts = append(parts, theme.Muted.Render(w.footer(l.innerWidth)))
	// Lip Gloss v2 Width includes padding and borders. Measure both frame and
	// content explicitly instead of clipping a larger dialog with MaxHeight.
	box := theme.Dialog.Padding(0, 1).Width(l.boxWidth).Render(strings.Join(parts, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (w *diagnosticWindow) footer(width int) string {
	action := "select"
	if w.detailFocus {
		action = "scroll"
	}
	for _, text := range []string{
		"Tab pane · ↑/↓ " + action + " · PgUp/PgDn · Home/End · c copy · Esc back",
		"Tab pane · ↑/↓ " + action + " · c copy · Esc back",
		"Tab pane · ↑/↓ · c copy · Esc back",
		"Tab pane · c copy · Esc back",
	} {
		if lipgloss.Width(text) <= width {
			return text
		}
	}
	return "Tab · c · Esc back"
}
