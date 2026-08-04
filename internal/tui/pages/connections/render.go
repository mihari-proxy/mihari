package connections

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func (m *Model) View() string {
	control := fmt.Sprintf("[%s %d | %s %d]  [%s: %s]  [%s]  [%s]",
		ui.ConnectionsActiveLabel, len(m.history.Active()), ui.ConnectionsClosedLabel, len(m.history.Closed()),
		ui.SourceIPLabel, m.source, ui.ColumnsLabel, m.pauseLabel())
	searchFocused := m.searching || m.focus.kind == focusSearch
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
		headers[index] = ui.ConnectionColumnLabel(column)
		if column == m.sortColumn {
			switch m.sortDirection {
			case sortDescending:
				headers[index] += " v"
			case sortAscending:
				headers[index] += " ^"
			}
		}
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
				line = m.theme.RowSelected.Render(line)
			}
			lines = append(lines, line)
		}
	}
	return lines
}

func (m *Model) tableWidth() int {
	width := 0
	for _, line := range m.tableLines() {
		width = max(width, utf8.RuneCountInString(line))
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
		if index == m.columnCursor {
			focus = ui.FocusMarker
		}
		lines = append(lines, focus+mark+" "+ui.ConnectionColumnLabel(column))
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
		runes := []rune(line)
		start := min(max(0, offset), len(runes))
		runes = runes[start:]
		if len(runes) > width {
			lines[index] = string(runes[:max(0, width-1)]) + "\u2026"
		} else {
			lines[index] = string(runes)
		}
	}
	return strings.Join(lines, "\n")
}

func formatBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", max(int64(0), value))
	}
	return fmt.Sprintf("%.1f KiB", float64(value)/1024)
}

func formatRate(value int64) string { return formatBytes(value) + "/s" }
