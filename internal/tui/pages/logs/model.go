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
	focusRow
)

type detailState struct {
	entry Entry
}

type Model struct {
	buffer       *Buffer
	focus        focusKind
	controlIndex int
	focused      int
	following    bool
	scrollUnread int
	level        string
	query        string
	searching    bool
	wrap         bool
	detail       *detailState
	stale        bool
	width        int
	height       int
	theme        ui.Theme
}

func New(capacity int) *Model {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	return &Model{buffer: NewBuffer(capacity), following: true, focus: focusControl, theme: ui.DefaultTheme()}
}

func (m *Model) ID() ui.PageID { return ui.PageLogs }

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
	case "/":
		m.searching = true
		return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputText} }
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
			if m.controlIndex == 0 {
				return m, func() tea.Msg { return ui.FocusRailMsg{} }
			}
			m.controlIndex--
		}
		return m, nil
	case "right":
		if m.focus == focusControl {
			m.controlIndex = min(3, m.controlIndex+1)
		}
		return m, nil
	case "up":
		if m.focus == focusRow {
			m.following = false
			if m.focused > 0 {
				m.focused--
			} else {
				m.focus = focusControl
			}
		}
		return m, nil
	case "down":
		entries := m.visibleEntries()
		if m.focus == focusControl {
			if len(entries) > 0 {
				m.focus = focusRow
				if m.following {
					m.focused = len(entries) - 1
				}
			}
		} else if m.focused+1 < len(entries) {
			m.focused++
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
	search := valueOr(m.query, ui.SearchPlaceholder)
	control := fmt.Sprintf("%s: %s  / %s  %s: %s  %s: %s", ui.LevelLabel, level, search, ui.WrapLabel, onOff(m.wrap), ui.PauseLabel, onOff(m.buffer.Paused()))
	if m.searching {
		control += "_"
	}
	if unread := m.Unread(); unread > 0 {
		control += fmt.Sprintf("  %s: %d", ui.UnreadLabel, unread)
	}
	if dropped := m.buffer.Dropped(); dropped > 0 {
		control += fmt.Sprintf("  %s: %d", ui.DroppedLabel, dropped)
	}
	if m.stale {
		control += "  " + ui.StaleLabel
	}
	lines := []string{m.theme.Title.Render(control), fmt.Sprintf("  %-8s  %-7s  %s", ui.TimeLabel, ui.LevelLabel, ui.MessageLabel)}
	entries := m.visibleEntries()
	if len(entries) == 0 {
		lines = append(lines, m.theme.Muted.Render(ui.NoMatchingLogs))
	} else {
		start, end := m.visibleWindow(len(entries))
		for index := start; index < end; index++ {
			lines = append(lines, m.renderEntry(entries[index], index == m.focused && m.focus == focusRow)...)
		}
	}
	content := strings.Join(lines, "\n")
	if m.detail != nil {
		content = m.renderDetail()
	}
	return content
}

func (m *Model) visibleEntries() []Entry {
	entries := m.buffer.Visible()
	result := make([]Entry, 0, len(entries))
	query := strings.ToLower(strings.TrimSpace(m.query))
	for _, entry := range entries {
		if m.level != "" && !strings.EqualFold(entry.Log.Level, m.level) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(entry.Log.Message), query) {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func (m *Model) visibleWindow(count int) (int, int) {
	rows := max(1, m.height-3)
	if count <= rows {
		return 0, count
	}
	if m.following {
		return count - rows, count
	}
	start := min(max(0, m.focused-rows+1), count-rows)
	return start, start + rows
}

func (m *Model) renderEntry(entry Entry, focused bool) []string {
	marker := "  "
	if focused {
		marker = "> "
	}
	timestamp := ui.MissingValue
	if !entry.ObservedAt.IsZero() {
		timestamp = entry.ObservedAt.Local().Format("15:04:05")
	}
	prefix := fmt.Sprintf("%s%-8s  %-7s  ", marker, timestamp, strings.ToUpper(safeLine(entry.Log.Level)))
	messageWidth := max(8, m.width-utf8.RuneCountInString(prefix)-1)
	message := safeLine(entry.Log.Message)
	if !m.wrap {
		return []string{prefix + truncate(message, messageWidth)}
	}
	wrapped := wrapText(message, messageWidth)
	lines := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		if index == 0 {
			lines = append(lines, prefix+line)
		} else {
			lines = append(lines, strings.Repeat(" ", utf8.RuneCountInString(prefix))+line)
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
	body := fmt.Sprintf("%s: %s\n%s: %s\n\n%s\n%s\n\n%s\n%s\n\n%s", ui.TimeLabel, timestamp, ui.LevelLabel, safe.Level, ui.MessageLabel, safe.Message, ui.RawTabLabel, raw, ui.EscCloseHint)
	content := m.theme.Dialog.Width(min(84, max(36, m.width-4))).Render(m.theme.Title.Render(ui.LogDetailsTitle) + "\n\n" + body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

func (m *Model) activateControl() tea.Cmd {
	switch m.controlIndex {
	case 0:
		m.level = cycleValue(m.level, []string{"debug", "info", "warning", "warn", "error"})
		m.reconcileFocus()
	case 1:
		m.searching = true
		return func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputText} }
	case 2:
		m.wrap = !m.wrap
	case 3:
		m.togglePause()
	}
	return nil
}

func (m *Model) updateSearch(message tea.Msg) (ui.Page, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "enter", "esc":
			m.searching = false
			m.reconcileFocus()
			return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
		}
	}
	value, handled, command := ui.EditTextField(m.query, message, 256)
	if handled {
		m.query = value
		return m, command
	}
	return m, nil
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
