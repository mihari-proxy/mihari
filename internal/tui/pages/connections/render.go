package connections

import (
	"fmt"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func (m *Model) View() string {
	controlFocused := m.contentFocused && m.focus.kind == focusControl
	control := ui.RenderControlStrip(m.theme, []string{
		fmt.Sprintf("[%s %d | %s %d]", ui.ConnectionsActiveLabel, len(m.history.Active()), ui.ConnectionsClosedLabel, len(m.history.Closed())),
		fmt.Sprintf("[%s: %s]", ui.SourceIPLabel, m.source),
		fmt.Sprintf("[%s]", ui.ColumnsLabel),
		fmt.Sprintf("[%s]", m.pauseLabel()),
	}, m.controlIndex, controlFocused, "  ")
	searchFocused := m.searching || (m.contentFocused && m.focus.kind == focusSearch)
	searchBar := ui.RenderSearchBar(m.theme, m.query, ui.SearchPlaceholder, searchFocused, m.width)
	lines := m.tableLines()
	base := clipLines(control, m.width) + "\n" + searchBar + "\n" + clipLinesAt(strings.Join(lines, "\n"), m.width, m.horizontalOffset)
	if m.columnsOpen {
		return base + "\n" + m.columnsView()
	}
	if m.detail != nil {
		return base + "\n" + m.detail.View(m.width, m.height)
	}
	return base
}

func (m *Model) tableLines() []string {
	rows := m.visibleRows()
	headers := make([]string, len(m.columns))
	for index, column := range m.columns {
		label := ui.ConnectionColumnLabel(column)
		if column == m.sortColumn {
			switch m.sortDirection {
			case sortDescending:
				label += " ↓"
			case sortAscending:
				label += " ↑"
			}
		}
		headerFocused := m.focus.kind == focusHeader && m.headerIndex == index
		headers[index] = ui.RenderHeaderCell(m.theme, label, headerFocused, m.contentFocused)
	}
	lines := []string{strings.Join(headers, "  ")}
	if len(rows) == 0 {
		lines = append(lines, m.theme.Muted.Render(ui.NoConnections))
	} else {
		for _, connection := range rows {
			values := make([]string, len(m.columns))
			for index, column := range m.columns {
				values[index] = columnValue(connection, column)
			}
			prefix := "  "
			rowFocused := m.focus.kind == focusRow && m.focus.rowID == connection.ID
			if rowFocused {
				prefix = ui.FocusMarker
			}
			line := prefix + strings.Join(values, "  ")
			if rowFocused && m.contentFocused {
				line = m.theme.RowFocus.Render(line)
			}
			lines = append(lines, line)
		}
	}
	return lines
}

func (m *Model) tableWidth() int {
	width := 0
	for _, line := range m.tableLines() {
		width = max(width, lipgloss.Width(line))
	}
	return width
}

func (m *Model) pauseLabel() string {
	if m.paused {
		return ui.ResumeLabel
	}
	return ui.PauseLabel
}

func (m *Model) columnsView() string {
	lines := []string{m.theme.Title.Render(ui.ColumnPickerTitle)}
	for index, column := range allColumnIDs {
		mark := "[ ]"
		if m.columnDraft[column] {
			mark = "[x]"
		}
		focus := "  "
		rowFocused := index == m.columnCursor
		if rowFocused {
			focus = ui.FocusMarker
		}
		line := focus + mark + " " + ui.ConnectionColumnLabel(column)
		if rowFocused && m.contentFocused {
			line = m.theme.RowFocus.Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(append(lines, ui.SaveLabel+" - Enter"), "\n")
}

func columnValue(connection protocol.Connection, column string) string {
	switch column {
	case "host":
		return value(connection.Metadata.Host)
	case "network":
		return value(connection.Metadata.Type) + "/" + value(connection.Metadata.Network)
	case "source":
		return address(connection.Metadata.SourceIP, connection.Metadata.SourcePort)
	case "destination":
		if connection.Metadata.Host != "" {
			return address(connection.Metadata.Host, connection.Metadata.DestinationPort)
		}
		return address(connection.Metadata.DestinationIP, connection.Metadata.DestinationPort)
	case "chain":
		return strings.Join(connection.Chains, " \u2192 ")
	case "rule":
		return strings.TrimSpace(connection.Rule + " " + connection.RulePay)
	case "process":
		return value(connection.Metadata.Process)
	case "upload":
		return formatBytes(connection.Upload)
	case "download":
		return formatBytes(connection.Download)
	case "traffic":
		return fmt.Sprintf("UL %s DL %s", formatRate(connection.UploadSpeed), formatRate(connection.DownloadSpeed))
	case "start":
		if connection.Start.IsZero() {
			return ui.MissingValue
		}
		return connection.Start.Local().Format("15:04:05")
	default:
		return ui.MissingValue
	}
}

func columnNumber(connection protocol.Connection, column string) int64 {
	switch column {
	case "upload":
		return connection.Upload
	case "download":
		return connection.Download
	case "traffic":
		return connection.UploadSpeed + connection.DownloadSpeed
	default:
		return 0
	}
}

func address(host, port string) string {
	if host == "" {
		return ui.MissingValue
	}
	if port == "" {
		return host
	}
	return host + ":" + port
}

func clipLines(content string, width int) string {
	return clipLinesAt(content, width, 0)
}

func clipLinesAt(content string, width, offset int) string {
	if width <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		// Clip by printable width so RowFocus / header ANSI does not inflate the budget.
		lines[index] = clipStyledLine(line, width, offset)
	}
	return strings.Join(lines, "\n")
}

// clipStyledLine pans and truncates a possibly styled line by visible columns.
func clipStyledLine(line string, width, offset int) string {
	if width <= 0 {
		return ""
	}
	if offset <= 0 && lipgloss.Width(line) <= width {
		return line
	}
	// Decompose into printable runes with preceding escape sequences.
	type cell struct {
		prefix string
		r      rune
	}
	cells := make([]cell, 0, len(line))
	var escape strings.Builder
	inEscape := false
	for _, r := range line {
		switch {
		case r == '\x1b':
			inEscape = true
			escape.WriteRune(r)
		case inEscape:
			escape.WriteRune(r)
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
		default:
			cells = append(cells, cell{prefix: escape.String(), r: r})
			escape.Reset()
		}
	}
	trailing := escape.String()

	start := min(max(0, offset), len(cells))
	rest := cells[start:]
	if len(rest) == 0 {
		return trailing
	}
	if len(rest) <= width {
		var out strings.Builder
		for _, c := range rest {
			out.WriteString(c.prefix)
			out.WriteRune(c.r)
		}
		out.WriteString(trailing)
		return out.String()
	}
	// Truncate: keep width-1 cells + ellipsis.
	keep := max(0, width-1)
	var out strings.Builder
	for _, c := range rest[:keep] {
		out.WriteString(c.prefix)
		out.WriteRune(c.r)
	}
	out.WriteString("\u2026")
	out.WriteString(trailing)
	return out.String()
}

func formatBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", max(int64(0), value))
	}
	return fmt.Sprintf("%.1f KiB", float64(value)/1024)
}

func formatRate(value int64) string { return formatBytes(value) + "/s" }
