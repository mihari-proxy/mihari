package proxies

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

const (
	proxyBarMaxWidth = 28
	delayTestURL     = "https://www.gstatic.com/generate_204"
	delayTimeout     = 5_000
)

type Client interface {
	SelectProxy(context.Context, string, protocol.ProxySelectionRequest) (protocol.MutationResult, error)
	DelayProxy(context.Context, string, protocol.DelayTestRequest) (protocol.DelayResult, error)
}

type DelayKind uint8

const (
	DelayUntested DelayKind = iota
	DelayTesting
	DelayValue
	DelayTimeout
)

type DelayState struct {
	Kind         DelayKind
	Milliseconds uint16
}

type Model struct {
	client         Client
	newOperationID func() string
	groups         []protocol.ProxyGroup
	expanded       map[string]bool
	focus          FocusID
	delays         map[string]DelayState
	pending        map[FocusID]bool
	contentFocused bool
	width          int
	height         int
	theme          ui.Theme
	now            time.Time // delay-test spinner clock
	delaySpinning  bool
	delaySpinGen   uint64
}

type selectionResultMsg struct {
	group string
	node  string
	err   error
}

type delayResultMsg struct {
	node  string
	delay uint16
	err   error
}

// delaySpinTickMsg advances braille frames while any node is DelayTesting.
type delaySpinTickMsg struct {
	t   time.Time
	gen uint64
}

type startDelaySpinMsg struct{ gen uint64 }

const delaySpinInterval = 100 * time.Millisecond

func New(client Client, newOperationID func() string) *Model {
	if newOperationID == nil {
		newOperationID = defaultOperationID
	}
	return &Model{
		client: client, newOperationID: newOperationID,
		expanded: make(map[string]bool), delays: make(map[string]DelayState), pending: make(map[FocusID]bool),
		theme: ui.DefaultTheme(),
	}
}

func (m *Model) ID() ui.PageID { return ui.PageProxies }

// SetContentFocused reports whether the root shell has given keyboard focus to this page.
func (m *Model) SetContentFocused(focused bool) { m.contentFocused = focused }

func (m *Model) SetSize(width, height int) { m.width, m.height = width, height }

func (m *Model) FocusFirst() {
	if len(m.groups) > 0 {
		m.focus = FocusID{Group: m.groups[0].Name}
	}
}

func (m *Model) SetGroups(groups protocol.ProxyGroups) {
	m.groups = append([]protocol.ProxyGroup(nil), groups.Groups...)
	for index := range m.groups {
		m.groups[index].All = append([]string(nil), groups.Groups[index].All...)
		m.groups[index].Nodes = append([]protocol.ProxyNode(nil), groups.Groups[index].Nodes...)
	}
	if m.groupIndex(m.focus.Group) < 0 {
		m.FocusFirst()
		return
	}
	if m.focus.Node != "" {
		group := m.groups[m.groupIndex(m.focus.Group)]
		if nodeIndex(group.Nodes, m.focus.Node) < 0 {
			m.focus = FocusID{Group: group.Name}
		}
	}
}

func (m *Model) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	switch typed := message.(type) {
	case selectionResultMsg:
		delete(m.pending, FocusID{Group: typed.group, Node: typed.node})
		if typed.err == nil {
			if index := m.groupIndex(typed.group); index >= 0 {
				m.groups[index].Now = typed.node
			}
		}
		return m, nil
	case delayResultMsg:
		if typed.err != nil {
			m.delays[typed.node] = DelayState{Kind: DelayTimeout}
		} else {
			m.delays[typed.node] = DelayState{Kind: DelayValue, Milliseconds: typed.delay}
		}
		return m, m.delaySpinCmdIfNeeded()
	case startDelaySpinMsg:
		if typed.gen != m.delaySpinGen || !m.hasTesting() {
			if typed.gen == m.delaySpinGen {
				m.delaySpinning = false
			}
			return m, nil
		}
		return m, tea.Tick(delaySpinInterval, func(t time.Time) tea.Msg {
			return delaySpinTickMsg{t: t, gen: typed.gen}
		})
	case delaySpinTickMsg:
		if typed.gen != m.delaySpinGen {
			return m, nil
		}
		m.now = typed.t
		if !m.hasTesting() {
			m.delaySpinning = false
			return m, nil
		}
		return m, tea.Tick(delaySpinInterval, func(t time.Time) tea.Msg {
			return delaySpinTickMsg{t: t, gen: typed.gen}
		})
	}
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if key.String() == "ctrl+t" {
		return m, m.testAll()
	}
	if m.focus.Node == "" {
		switch key.String() {
		case "enter":
			m.expanded[m.focus.Group] = !m.expanded[m.focus.Group]
		case "left":
			return m, func() tea.Msg { return ui.FocusRailMsg{} }
		case "up", "down":
			m.move(key.String())
		}
		return m, nil
	}
	switch key.String() {
	case "left", "right", "up", "down":
		m.move(key.String())
	case "enter":
		return m, m.selectFocused()
	case "t":
		return m, tea.Batch(m.testNode(m.focus.Node), m.delaySpinCmdIfNeeded())
	}
	return m, nil
}

func (m *Model) View() string {
	if len(m.groups) == 0 {
		return m.theme.Muted.Render(ui.NoProxyGroups)
	}
	sections := make([]string, 0, len(m.groups))
	for _, group := range m.groups {
		marker := "▸"
		if m.expanded[group.Name] {
			marker = "▾"
		}
		focus := "  "
		groupFocused := m.focus == (FocusID{Group: group.Name})
		if groupFocused {
			focus = ui.FocusMarker
		}
		header := fmt.Sprintf("%s%s %s  %s · %d", focus, marker, group.Name, strings.ToUpper(group.Type), len(group.Nodes))
		switch {
		case groupFocused && m.contentFocused:
			header = m.theme.RowFocus.Render(header)
		case !groupFocused:
			header = m.theme.Title.Render(header)
		}
		lines := []string{header}
		if m.expanded[group.Name] {
			lines = append(lines, m.renderNodes(group)...)
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	return strings.Join(sections, "\n\n")
}

func (m *Model) renderNodes(group protocol.ProxyGroup) []string {
	columns := m.columns()
	barWidth := min(proxyBarMaxWidth, max(18, m.width/columns-1))
	rows := make([]string, 0, (len(group.Nodes)+columns-1)/columns)
	for start := 0; start < len(group.Nodes); start += columns {
		bars := make([]string, 0, columns)
		for index := start; index < min(start+columns, len(group.Nodes)); index++ {
			bars = append(bars, m.renderNode(group, group.Nodes[index], barWidth))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, bars...))
	}
	return rows
}

func (m *Model) renderNode(group protocol.ProxyGroup, node protocol.ProxyNode, width int) string {
	id := FocusID{Group: group.Name, Node: node.Name}
	focus := "  "
	if m.focus == id {
		focus = ui.FocusMarker
	}
	selected := " "
	if group.Now == node.Name {
		selected = "✓"
	}
	if m.pending[id] {
		selected = "…"
	}
	metadata := strings.ToUpper(node.Type)
	if metadata == "" {
		metadata = ui.MissingValue
	}
	if node.XUDP {
		metadata += " / XUDP"
	} else if node.UDP {
		metadata += " / UDP"
	}
	content := fmt.Sprintf("%s%s %s\n%s  %s", focus, selected, node.Name, metadata, renderDelay(m.theme, m.delays[node.Name], m.now))
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(width)
	// Accent the focused node only while content owns keyboard focus.
	if m.focus == id && m.contentFocused {
		style = style.BorderForeground(m.theme.ColorAccent)
	}
	return style.Render(content)
}

func (m *Model) columns() int {
	if m.width < 56 {
		return 1
	}
	return max(1, m.width/proxyBarMaxWidth)
}

func (m *Model) selectFocused() tea.Cmd {
	if m.client == nil || m.focus.Node == "" {
		return nil
	}
	id := m.focus
	m.pending[id] = true
	operationID := m.newOperationID()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := m.client.SelectProxy(ctx, id.Group, protocol.ProxySelectionRequest{OperationID: operationID, Name: id.Node})
		return selectionResultMsg{group: id.Group, node: id.Node, err: err}
	}
}

func (m *Model) testNode(node string) tea.Cmd {
	if m.client == nil || node == "" {
		return nil
	}
	m.delays[node] = DelayState{Kind: DelayTesting}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, err := m.client.DelayProxy(ctx, node, protocol.DelayTestRequest{URL: delayTestURL, TimeoutMilliseconds: delayTimeout})
		return delayResultMsg{node: node, delay: result.Delays[node], err: err}
	}
}

func (m *Model) testAll() tea.Cmd {
	seen := make(map[string]struct{})
	commands := make([]tea.Cmd, 0)
	for _, group := range m.groups {
		for _, node := range group.Nodes {
			if _, exists := seen[node.Name]; exists {
				continue
			}
			seen[node.Name] = struct{}{}
			commands = append(commands, m.testNode(node.Name))
		}
	}
	if spin := m.delaySpinCmdIfNeeded(); spin != nil {
		commands = append(commands, spin)
	}
	return tea.Batch(commands...)
}

func (m *Model) hasTesting() bool {
	for _, delay := range m.delays {
		if delay.Kind == DelayTesting {
			return true
		}
	}
	return false
}

// delaySpinCmdIfNeeded starts a generation-owned spin loop while any delay test is in flight.
func (m *Model) delaySpinCmdIfNeeded() tea.Cmd {
	if !m.hasTesting() {
		m.delaySpinning = false
		return nil
	}
	m.delaySpinGen++
	gen := m.delaySpinGen
	m.delaySpinning = true
	return func() tea.Msg { return startDelaySpinMsg{gen: gen} }
}

// delayStyle maps latency state onto the theme color ladder.
// Bands (ms): <100 good, 100–399 mid, ≥400 bad; timeout → DelayBad; untested → Muted; testing → Warning.
func delayStyle(theme ui.Theme, delay DelayState) lipgloss.Style {
	switch delay.Kind {
	case DelayTesting:
		return theme.Warning
	case DelayTimeout:
		return theme.DelayBad
	case DelayValue:
		switch {
		case delay.Milliseconds < 100:
			return theme.DelayGood
		case delay.Milliseconds < 400:
			return theme.DelayMid
		default:
			return theme.DelayBad
		}
	default:
		return theme.Muted
	}
}

func renderDelay(theme ui.Theme, delay DelayState, now time.Time) string {
	style := delayStyle(theme, delay)
	switch delay.Kind {
	case DelayTesting:
		if now.IsZero() {
			now = time.Unix(0, 0)
		}
		// Braille spinner + "Testing" (not static Testing…).
		return style.Render(ui.SpinnerLabel(now, "Testing"))
	case DelayValue:
		return style.Render(fmt.Sprintf("%d ms", delay.Milliseconds))
	case DelayTimeout:
		return style.Render(ui.TimeoutLabel)
	default:
		return style.Render(ui.MissingValue)
	}
}

var fallbackOperationID atomic.Uint64

func defaultOperationID() string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err == nil {
		return "tui-" + hex.EncodeToString(raw)
	}
	return fmt.Sprintf("tui-%d", fallbackOperationID.Add(1))
}
