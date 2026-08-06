package connections

import (
	"fmt"
	"strings"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func (m *Model) View() string {
	controlFocused := m.contentFocused && m.focus.kind == focusControl
	activeN, closedN := len(m.history.Active()), len(m.history.Closed())
	control := ui.RenderControlStrip(m.theme, []string{
		fmt.Sprintf("[%s %d | %s %d]", ui.ConnectionsActiveLabel, activeN, ui.ConnectionsClosedLabel, closedN),
		fmt.Sprintf("[%s: %s]", ui.SourceIPLabel, m.source),
		fmt.Sprintf("[%s]", ui.ColumnsLabel),
		fmt.Sprintf("[%s]", m.pauseLabel()),
	}, m.controlIndex, controlFocused, "  ")
	searchFocused := m.searching || (m.contentFocused && m.focus.kind == focusSearch)
	// Search bar width is the section body text width, not the raw page width.
	inner := ui.FullSectionInner(m.layoutWidth())
	textW := ui.SectionTextWidth(inner)
	searchBar := ui.RenderSearchBar(m.theme, m.query, ui.SearchPlaceholder, searchFocused, m.queryCursor, textW)
	controlsBody := clipLines(control, textW) + "\n" + searchBar
	controls := ui.RenderBorderedSection(m.theme, ui.ControlsSectionTitle, controlsBody, inner)

	listN := activeN
	activeMode := m.dataset == datasetActive
	if !activeMode {
		listN = closedN
	}
	listTitle := ui.FormatConnectionsTitle(activeMode, listN)
	listBody := clipLines(strings.Join(m.tableLines(), "\n"), textW)
	list := ui.RenderBorderedSection(m.theme, listTitle, listBody, inner)

	base := controls + "\n" + list
	if m.columnsOpen {
		return m.columnsView()
	}
	if m.detail != nil {
		return m.detail.View(m.width, m.height)
	}
	return base
}

// connectionChrome is the page chrome outside table rows: Controls section
// (top + title + control strip + search + bottom = 5) plus List section
// (top + title + header + rule + bottom = 5 minus the header/rule lines that
// are part of tableLines itself; effective 9).
const connectionChrome = 9

func (m *Model) tableLines() []string {
	rows := m.visibleRows()
	header, rule := m.connectionHeader()
	lines := []string{header, rule}
	if len(rows) == 0 {
		return append(lines, m.theme.Muted.Render(ui.NoConnections))
	}
	// Window the rows; focused row stays inside, header focus pins to the top.
	focusedIndex := 0
	if m.focus.kind == focusRow {
		focusedIndex = max(0, rowIndex(rows, m.focus.rowID))
	}
	start, end := ui.VisibleWindow(len(rows), m.height, connectionChrome, false, focusedIndex)
	for index := start; index < end; index++ {
		connection := rows[index]
		rowFocused := m.focus.kind == focusRow && m.focus.rowID == connection.ID
		lines = append(lines, m.renderConnection(connection, rowFocused)...)
	}
	return lines
}

func (m *Model) connectionHeader() (string, string) {
	cols, widths := m.keptConnectionColumns()
	if len(cols) == 0 {
		return "  ", "  "
	}
	// Sort indicator: ▲ ascending / ▼ descending on the sorted column.
	if m.sortDirection != sortDefault {
		for index := range cols {
			if cols[index].ID != m.sortColumn {
				continue
			}
			if m.sortDirection == sortAscending {
				cols[index].Title += " ▲"
			} else {
				cols[index].Title += " ▼"
			}
			break
		}
	}
	// headerIndex addresses the kept set; highlight only while focused on it.
	focusedIndex := -1
	if m.focus.kind == focusHeader {
		focusedIndex = m.headerIndex
	}
	header, rule := ui.RenderHeaderRow(m.theme, cols, widths, 2, focusedIndex, m.contentFocused)
	return "  " + header, "  " + rule
}

func (m *Model) layoutWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 100
}

// connectionColumns maps the checked column set to table column definitions.
// Priority is positional-reverse (host is never dropped, tail columns drop
// first), so the checked order, table order and drop order are one.
func (m *Model) connectionColumns() []ui.TableColumn {
	specs := map[string]ui.TableColumn{
		"host":        {MinWidth: 12, Flex: 3, Priority: 0},
		"traffic":     {MinWidth: 16, MaxWidth: 24, Flex: 1, Align: ui.AlignRight, Priority: 10},
		"network":     {MinWidth: 10, Flex: 1, Priority: 9},
		"rule":        {MinWidth: 12, Flex: 2, Priority: 8},
		"start":       {MinWidth: 10, Flex: 1, Priority: 7},
		"process":     {MinWidth: 12, Flex: 1, Priority: 6},
		"chain":       {MinWidth: 12, Flex: 2, Priority: 5},
		"source":      {MinWidth: 14, Flex: 1, Priority: 4},
		"destination": {MinWidth: 14, Flex: 1, Priority: 3},
		"upload":      {MinWidth: 10, Flex: 1, Align: ui.AlignRight, Priority: 2},
		"download":    {MinWidth: 10, Flex: 1, Align: ui.AlignRight, Priority: 1},
	}
	cols := make([]ui.TableColumn, 0, len(m.columns))
	for _, id := range m.columns {
		spec := specs[id]
		spec.ID = id
		spec.Title = ui.ConnectionColumnLabel(id)
		cols = append(cols, spec)
	}
	return cols
}

// keptConnectionColumns fits the checked columns into the section body width,
// dropping low-priority tail columns that no longer fit (design §2.4).
func (m *Model) keptConnectionColumns() ([]ui.TableColumn, []int) {
	textW := ui.SectionTextWidth(ui.FullSectionInner(m.layoutWidth()))
	avail := max(24, textW-2) // focus marker budget inside section
	return ui.FitPriorityColumns(m.connectionColumns(), avail, 2)
}

// headerColumnCount is the number of columns visible on screen; headerIndex
// walks this set so focus and sort never land on a dropped column.
func (m *Model) headerColumnCount() int {
	cols, _ := m.keptConnectionColumns()
	return len(cols)
}

func (m *Model) renderConnection(connection protocol.Connection, focused bool) []string {
	cols, widths := m.keptConnectionColumns()
	marker := ui.FocusPrefix(focused)
	cells := make([]string, 0, len(cols))
	for index, col := range cols {
		var text string
		switch col.ID {
		case "traffic":
			up := "↑" + formatRate(connection.UploadSpeed)
			down := "↓" + formatRate(connection.DownloadSpeed)
			text = ui.StyleTrafficPair(m.theme, up, down)
		case "network":
			text = ui.StyleNetwork(m.theme, columnValue(connection, col.ID))
		default:
			text = columnValue(connection, col.ID)
		}
		cells = append(cells, ui.PadCell(text, widths[index], col.Align))
	}
	line := marker + ui.JoinCells(cells, 2)
	if focused && m.contentFocused {
		line = m.theme.RowFocus.Render(line)
	}
	return []string{line}
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
		return fmt.Sprintf("↑%s ↓%s", formatRate(connection.UploadSpeed), formatRate(connection.DownloadSpeed))
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

// clipLines clips every line of content to width visible columns, keeping
// ANSI style prefixes intact (truncateStyled semantics).
func clipLines(content string, width int) string {
	if width <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		lines[index] = ui.TruncateVisible(line, width)
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
