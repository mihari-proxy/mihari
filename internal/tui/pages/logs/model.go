package logs

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

const defaultCapacity = 10_000

type focusKind uint8

const (
	focusControl focusKind = iota
	focusSearch
	focusRow
)

type detailState struct {
	entry Entry
}

type Model struct {
	buffer         *Buffer
	focus          focusKind
	controlIndex   int
	focused        int
	following      bool
	scrollUnread   int
	level          string
	query          string
	queryCursor    int
	searching      bool
	wrap           bool
	detail         *detailState
	stale          bool
	contentFocused bool
	width          int
	height         int
	theme          ui.Theme
}

func New(capacity int) *Model {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	return &Model{buffer: NewBuffer(capacity), following: true, focus: focusControl, theme: ui.DefaultTheme()}
}

func (m *Model) ID() ui.PageID { return ui.PageLogs }

// SetContentFocused reports whether the root shell has given keyboard focus to this page.
func (m *Model) SetContentFocused(focused bool) { m.contentFocused = focused }

// FooterHints returns contextual shortcuts for the root shell footer.
func (m *Model) FooterHints() string {
	switch {
	case m.detail != nil:
		return ui.FooterDetailMode
	case m.searching:
		return ui.FooterSearchMode
	default:
		return ui.FooterLogs
	}
}

func (m *Model) SetSize(width, height int) { m.width, m.height = width, height }

func (m *Model) FocusFirst() { m.focus, m.controlIndex = focusControl, 0 }

func (m *Model) SetStale(stale bool) { m.stale = stale }

func (m *Model) Append(entry Entry) {
	dropped := m.buffer.Append(entry)
	if dropped && !m.following && m.focused > 0 {
		m.focused--
	}
	if !m.following && !m.buffer.Paused() {
		m.scrollUnread++
	}
	if m.following && !m.buffer.Paused() {
		m.focused = max(0, m.visibleCount()-1)
	}
}

func (m *Model) visibleCount() int {
	if m.level == "" && strings.TrimSpace(m.query) == "" {
		return m.buffer.Len()
	}
	return len(m.visibleEntries())
}

func (m *Model) Observe(entry protocol.LogEntry, observedAt time.Time) {
	m.Append(Entry{ObservedAt: observedAt, Log: entry})
}

func (m *Model) SetFilter(level, query string) {
	m.level, m.query = level, query
	m.reconcileFocus()
}

func (m *Model) Unread() int { return m.scrollUnread + m.buffer.Unread() }

func (m *Model) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	if m.searching {
		return m.updateSearch(message)
	}
	// Search bar focus is a character-input surface: typing starts filter without Enter.
	// Page shortcuts (p, w, /…) are disabled here so printable keys edit the query.
	if m.detail == nil && m.focus == focusSearch {
		return m.updateSearchFocus(message)
	}
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if m.detail != nil {
		if key.String() == "esc" || key.String() == "enter" {
			m.detail = nil
		}
		return m, nil
	}
	switch key.String() {
	case "esc":
		return m, func() tea.Msg { return ui.FocusRailMsg{} }
	case "/":
		return m, m.startSearch()
	case "p":
		m.togglePause()
		return m, nil
	case "w":
		m.wrap = !m.wrap
		return m, nil
	case "G":
		m.following = true
		m.scrollUnread = 0
		m.focus = focusRow
		m.focused = max(0, len(m.visibleEntries())-1)
		return m, nil
	case "left":
		if m.focus == focusControl {
			m.controlIndex = max(0, m.controlIndex-1)
		}
		return m, nil
	case "right":
		if m.focus == focusControl {
			m.controlIndex = min(2, m.controlIndex+1)
		}
		return m, nil
	case "up":
		switch m.focus {
		case focusRow:
			m.following = false
			if m.focused > 0 {
				m.focused--
			} else {
				return m, m.startSearch()
			}
		}
		return m, nil
	case "down":
		entries := m.visibleEntries()
		switch m.focus {
		case focusControl:
			return m, m.startSearch()
		case focusRow:
			if m.focused+1 < len(entries) {
				m.focused++
			}
		}
		return m, nil
	case "enter":
		if m.focus == focusControl {
			return m, m.activateControl()
		}
		entries := m.visibleEntries()
		if m.focused >= 0 && m.focused < len(entries) {
			m.detail = &detailState{entry: entries[m.focused]}
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) View() string {
	level := valueOr(m.level, ui.FilterAllLabel)
	controlFocused := m.contentFocused && m.focus == focusControl
	control := ui.RenderControlStrip(m.theme, []string{
		fmt.Sprintf("%s: %s", ui.LevelLabel, ui.StyleLogLevel(m.theme, level)),
		fmt.Sprintf("%s: %s", ui.WrapLabel, ui.StatusDot(m.theme, ui.ClassifyStatusTone(onOff(m.wrap)), onOff(m.wrap))),
		fmt.Sprintf("%s: %s", ui.PauseLabel, ui.StatusDot(m.theme, ui.ClassifyStatusTone(onOff(m.buffer.Paused())), onOff(m.buffer.Paused()))),
	}, m.controlIndex, controlFocused, "  ")
	var status []string
	if unread := m.Unread(); unread > 0 {
		status = append(status, fmt.Sprintf("%s: %d", ui.UnreadLabel, unread))
	}
	if dropped := m.buffer.Dropped(); dropped > 0 {
		status = append(status, fmt.Sprintf("%s: %d", ui.DroppedLabel, dropped))
	}
	if m.stale {
		status = append(status, ui.StaleLabel)
	}
	if len(status) > 0 {
		control += "  " + m.theme.Muted.Render(strings.Join(status, "  "))
	}
	inner := ui.FullSectionInner(m.layoutWidth())
	textW := ui.SectionTextWidth(inner)
	searchFocused := m.searching || (m.contentFocused && m.focus == focusSearch)
	searchBar := ui.RenderSearchBar(m.theme, m.query, ui.SearchPlaceholder, searchFocused, m.queryCursor, textW)
	controlsBody := control + "\n" + searchBar
	controls := ui.RenderBorderedSection(m.theme, ui.ControlsSectionTitle, controlsBody, inner)

	widths := m.logColumnWidths()
	header, rule := ui.RenderHeaderRow(m.theme, m.logColumns(), widths, logColGap, -1, false)
	// Indent header under focus marker column so it lines up with data.
	indent := "  "
	listLines := []string{indent + header, indent + rule}
	entries := m.visibleEntries()
	if len(entries) == 0 {
		listLines = append(listLines, m.theme.Muted.Render(ui.NoMatchingLogs))
	} else {
		start, end := ui.VisibleWindow(len(entries), m.height, logChrome, m.following, m.focused)
		for index := start; index < end; index++ {
			listLines = append(listLines, m.renderEntry(entries[index], index == m.focused && m.focus == focusRow)...)
		}
	}
	list := ui.RenderBorderedSection(m.theme, ui.LogsSectionTitle, strings.Join(listLines, "\n"), inner)

	content := controls + "\n" + list
	if m.detail != nil {
		content = m.renderDetail()
	}
	return content
}

const logColGap = 2

func (m *Model) layoutWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 100
}

func (m *Model) logColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{ID: "time", Title: ui.TimeLabel, MinWidth: 8, MaxWidth: 8, Flex: 0},
		{ID: "level", Title: ui.LevelLabel, MinWidth: 7, MaxWidth: 8, Flex: 0},
		{ID: "message", Title: ui.MessageLabel, MinWidth: 8, Flex: 1},
	}
}

func (m *Model) logColumnWidths() []int {
	// Fit columns inside the Logs section body text width.
	textW := ui.SectionTextWidth(ui.FullSectionInner(m.layoutWidth()))
	avail := max(20, textW-2) // focus marker budget
	return ui.FitColumnWidths(m.logColumns(), avail, logColGap)
}

func (m *Model) visibleEntries() []Entry {
	entries := m.buffer.Visible()
	result := make([]Entry, 0, len(entries))
	visible := []string{"time", "level", "message"}
	for _, entry := range entries {
		if m.level != "" && !strings.EqualFold(entry.Log.Level, m.level) {
			continue
		}
		timestamp := ui.MissingValue
		if !entry.ObservedAt.IsZero() {
			timestamp = entry.ObservedAt.Local().Format("15:04:05")
		}
		cells := map[string]string{
			"time":    timestamp,
			"level":   entry.Log.Level,
			"message": entry.Log.Message,
		}
		if !ui.MatchVisibleColumns(cells, visible, m.query) {
			continue
		}
		result = append(result, entry)
	}
	return result
}

// logChrome is the page chrome outside log rows: Controls section
// (top + control + search + bottom = 4) plus Logs section (top + header +
// rule + bottom = 4), leaving the rest for data rows.
const logChrome = 8

func (m *Model) renderEntry(entry Entry, focused bool) []string {
	marker := ui.FocusPrefix(focused)
	widths := m.logColumnWidths()
	timestamp := ui.MissingValue
	if !entry.ObservedAt.IsZero() {
		timestamp = entry.ObservedAt.Local().Format("15:04:05")
	}
	timeCell := ui.PadCell(timestamp, widths[0], ui.AlignLeft)
	// Semantic level colors are always applied; only RowFocus chrome waits on content focus.
	levelText := ui.StyleLogLevel(m.theme, safeLine(entry.Log.Level))
	levelCell := ui.PadCell(levelText, widths[1], ui.AlignLeft)
	messageWidth := widths[2]
	message := safeLine(entry.Log.Message)
	styleLine := func(line string) string {
		if focused && m.contentFocused {
			return ui.ApplyFocusStyle(line, m.theme.RowFocus)
		}
		return line
	}
	meta := ui.JoinCells([]string{timeCell, levelCell}, logColGap)
	if !m.wrap {
		msg := ui.PadCell(message, messageWidth, ui.AlignLeft)
		return []string{styleLine(marker + ui.JoinCells([]string{meta, msg}, logColGap))}
	}
	wrapped := wrapText(message, messageWidth)
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	lines := make([]string, 0, len(wrapped))
	padMeta := lipgloss.Width(marker) + lipgloss.Width(meta) + logColGap
	for index, line := range wrapped {
		if index == 0 {
			msg := ui.PadCell(line, messageWidth, ui.AlignLeft)
			lines = append(lines, styleLine(marker+ui.JoinCells([]string{meta, msg}, logColGap)))
		} else {
			lines = append(lines, styleLine(strings.Repeat(" ", padMeta)+ui.PadCell(line, messageWidth, ui.AlignLeft)))
		}
	}
	return lines
}

func (m *Model) renderDetail() string {
	entry := m.detail.entry
	safe := protocol.LogEntry{Level: safeLine(entry.Log.Level), Message: safeMultiline(entry.Log.Message)}
	raw := ui.UnavailableTitle
	if encoded, err := json.MarshalIndent(struct {
		ObservedAt time.Time `json:"observed_at,omitzero"`
		Level      string    `json:"type"`
		Message    string    `json:"payload"`
	}{ObservedAt: entry.ObservedAt, Level: safe.Level, Message: safe.Message}, "", "  "); err == nil {
		raw = string(encoded)
	}
	timestamp := ui.MissingValue
	if !entry.ObservedAt.IsZero() {
		timestamp = entry.ObservedAt.Local().Format(time.RFC3339)
	}
	body := fmt.Sprintf("%s: %s\n%s: %s\n\n%s\n%s\n\n%s\n%s\n\n%s", ui.TimeLabel, timestamp, ui.LevelLabel, ui.StyleLogLevel(m.theme, safe.Level), ui.MessageLabel, safe.Message, ui.RawTabLabel, raw, ui.EscCloseHint)
	content := m.theme.Dialog.Width(min(84, max(36, m.width-4))).Render(m.theme.Title.Render(ui.LogDetailsTitle) + "\n\n" + body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

func (m *Model) activateControl() tea.Cmd {
	switch m.controlIndex {
	case 0:
		m.level = cycleValue(m.level, []string{"debug", "info", "warning", "warn", "error"})
		m.reconcileFocus()
	case 1:
		m.wrap = !m.wrap
	case 2:
		m.togglePause()
	}
	return nil
}

func (m *Model) updateSearchFocus(message tea.Msg) (ui.Page, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "up":
			m.focus = focusControl
			return m, nil
		case "down":
			entries := m.visibleEntries()
			if len(entries) > 0 {
				m.focus = focusRow
				if m.following {
					m.focused = len(entries) - 1
				}
			}
			return m, nil
		}
	}
	if ui.IsTextEditMsg(message) {
		enter := m.startSearch()
		page, edit := m.updateSearch(message)
		return page, tea.Batch(enter, edit)
	}
	return m, nil
}

func (m *Model) updateSearch(message tea.Msg) (ui.Page, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			m.searching = false
			m.reconcileFocus()
			return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
		case "up":
			m.searching = false
			m.focus = focusControl
			m.reconcileFocus()
			return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
		case "down":
			m.searching = false
			entries := m.visibleEntries()
			if len(entries) > 0 {
				m.focus = focusRow
				if m.following {
					m.focused = len(entries) - 1
				}
			}
			m.reconcileFocus()
			return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
		}
	}
	value, cursor, handled, command := ui.EditTextField(m.query, m.queryCursor, message, 256)
	if handled {
		m.query = value
		m.queryCursor = cursor
		return m, command
	}
	return m, nil
}

func (m *Model) startSearch() tea.Cmd {
	m.searching = true
	m.focus = focusSearch
	m.queryCursor = utf8.RuneCountInString(m.query)
	return func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputText} }
}

func (m *Model) togglePause() {
	if m.buffer.Paused() {
		unread := m.buffer.Unread()
		m.buffer.Resume()
		if m.following {
			m.scrollUnread = 0
			m.focused = max(0, len(m.visibleEntries())-1)
		} else {
			m.scrollUnread += unread
		}
		return
	}
	m.buffer.Pause()
}

func (m *Model) reconcileFocus() {
	count := len(m.visibleEntries())
	if count == 0 {
		m.focus = focusControl
		m.focused = 0
		return
	}
	if m.following {
		m.focused = count - 1
	} else {
		m.focused = min(m.focused, count-1)
	}
}

func safeLine(value string) string {
	return strings.Join(strings.FieldsFunc(safeMultiline(value), func(r rune) bool { return r == '\n' || r == '\r' || r == '\t' }), " ")
}

func safeMultiline(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if r == '\n' || r == '\t' || (!unicode.IsControl(r) && r != '\x1b') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func wrapText(value string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	runes := []rune(value)
	if len(runes) == 0 {
		return []string{""}
	}
	lines := make([]string, 0, (len(runes)+width-1)/width)
	for len(runes) > 0 {
		end := min(width, len(runes))
		lines = append(lines, string(runes[:end]))
		runes = runes[end:]
	}
	return lines
}

func truncate(value string, width int) string {
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width <= 1 {
		return string(runes[:max(0, width)])
	}
	return string(runes[:width-1]) + "…"
}

func cycleValue(current string, values []string) string {
	if current == "" {
		return values[0]
	}
	for index, value := range values {
		if strings.EqualFold(current, value) {
			if index+1 == len(values) {
				return ""
			}
			return values[index+1]
		}
	}
	return values[0]
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func onOff(value bool) string {
	if value {
		return ui.OnLabel
	}
	return ui.OffLabel
}
