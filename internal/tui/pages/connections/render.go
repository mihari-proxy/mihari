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
		return base + "\n" + m.columnsView()
	}
	if m.detail != nil {
		return base + "\n" + m.detail.View(m.width, m.height)
	}
	return base
}

func (m *Model) tableLines() []string {
	rows := m.visibleRows()
	header, rule := m.connectionHeader()
	lines := []string{header, rule}
	if len(rows) == 0 {
		return append(lines, m.theme.Muted.Render(ui.NoConnections))
	}
	for _, connection := range rows {
		rowFocused := m.focus.kind == focusRow && m.focus.rowID == connection.ID
		lines = append(lines, m.renderConnection(connection, rowFocused)...)
	}
	return lines
}

func (m *Model) connectionHeader() (string, string) {
	// Dual-line cards: short guide headers rather than every preference column.
	titles := []string{ui.ConnectionColumnLabel("host"), ui.ConnectionColumnLabel("traffic")}
	widths := m.connectionPrimaryWidths()
	header, rule := ui.RenderHeaderRow(m.theme, titles, widths, 2, -1, false)
	return "  " + header, "  " + rule
}

func (m *Model) layoutWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 100
}

func (m *Model) connectionPrimaryWidths() []int {
	// Fit columns to the section body text width (page minus shell + section chrome).
	textW := ui.SectionTextWidth(ui.FullSectionInner(m.layoutWidth()))
	avail := max(24, textW-2) // focus marker budget inside section
	return ui.FitColumnWidths([]ui.TableColumn{
		{ID: "host", MinWidth: 12, Flex: 3},
		{ID: "traffic", MinWidth: 14, MaxWidth: 24, Flex: 1, Align: ui.AlignRight},
	}, avail, 2)
}

func (m *Model) renderConnection(connection protocol.Connection, focused bool) []string {
	marker := ui.FocusPrefix(focused)
	widths := m.connectionPrimaryWidths()
	host := primaryHost(connection)
	up := "↑" + formatRate(connection.UploadSpeed)
	down := "↓" + formatRate(connection.DownloadSpeed)
	// Semantic traffic colors always; RowFocus chrome stays content-focus gated.
	traffic := ui.StyleTrafficPair(m.theme, up, down)
	hostCell := ui.PadCell(host, widths[0], ui.AlignLeft)
	// Traffic is pre-styled; pad by visible width toward the right.
	trafficPad := widths[1] - lipgloss.Width(traffic)
	if trafficPad < 0 {
		traffic = ui.TruncateVisible(traffic, widths[1])
		trafficPad = 0
	}
	trafficCell := strings.Repeat(" ", trafficPad) + traffic
	primary := marker + ui.JoinCells([]string{hostCell, trafficCell}, 2)

	secondary := m.secondaryLine(connection)
	// Align secondary under host cell (after marker); clip by visible width.
	secondaryLine := "  " + ui.TruncateVisible(secondary, max(8, m.layoutWidth()-2))

	if focused && m.contentFocused {
		primary = m.theme.RowFocus.Render(primary)
	}
	return []string{primary, secondaryLine}
}

func (m *Model) secondaryLine(connection protocol.Connection) string {
	// Secondary metadata uses semantic/muted colors whenever the page is visible.
	sep := m.theme.Muted.Render("  ·  ")
	net := ui.StyleNetwork(m.theme, networkLabel(connection))
	parts := []string{net}
	chain := strings.Join(connection.Chains, " → ")
	if chain == "" {
		chain = ui.MissingValue
	}
	parts = append(parts, m.theme.Muted.Render(chain))
	if ui.ClassifyContentWidth(m.layoutWidth()) == ui.ContentFull {
		rule := strings.TrimSpace(connection.Rule + " " + connection.RulePay)
		if rule != "" {
			parts = append(parts, m.theme.Muted.Render(rule))
		}
		if process := connection.Metadata.Process; process != "" {
			parts = append(parts, m.theme.Muted.Render(process))
		}
	}
	if !connection.Start.IsZero() {
		parts = append(parts, m.theme.Muted.Render(connection.Start.Local().Format("15:04:05")))
	}
	return strings.Join(parts, sep)
}

func primaryHost(connection protocol.Connection) string {
	if connection.Metadata.Host != "" {
		return address(connection.Metadata.Host, connection.Metadata.DestinationPort)
	}
	if connection.Metadata.DestinationIP != "" {
		return address(connection.Metadata.DestinationIP, connection.Metadata.DestinationPort)
	}
	return value(connection.Metadata.Process)
}

func networkLabel(connection protocol.Connection) string {
	typ := value(connection.Metadata.Type)
	net := value(connection.Metadata.Network)
	if typ == ui.MissingValue && net == ui.MissingValue {
		return ui.MissingValue
	}
	if typ == ui.MissingValue {
		return net
	}
	if net == ui.MissingValue {
		return typ
	}
	return typ + "/" + net
}

func (m *Model) tableWidth() int {
	// Dual-line layout no longer pans a mega-row; keep helper for callers.
	return m.width
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
