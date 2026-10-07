package proxies

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type routingUI struct {
	available, known, open, pending bool
	epoch                           uint64
	status                          protocol.RoutingStatus
	focus, cursor                   int // focus: 0 Mode, 1 GLOBAL, 2 Page Settings, -1 groups
	err                             string
}

type routingClient interface {
	UpdateRouting(context.Context, protocol.RoutingUpdateRequest) (protocol.RoutingStatus, error)
}
type routingResultMsg struct {
	cancelled bool
	status    protocol.RoutingStatus
	epoch     uint64
	err       error
}

func (r routingResultMsg) Err() error { return r.err }

var routingModes = []string{"rule", "global", "direct"}
var routingLabels = []string{"Rule", "Global", "Direct"}
var routingDescriptions = []string{"Follow routing rules", "Use the GLOBAL selection", "Connect directly"}

// routingLabelStyle uses the reference's white labels without changing the
// shared theme; reverse video turns this white foreground into the focus fill.
var routingLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Width(9)

// SetRoutingAvailable updates capability and daemon session identity.
func (m *Model) SetRoutingAvailable(available bool, epoch uint64) {
	if epoch < m.routing.epoch {
		return
	}
	if epoch != m.routing.epoch || available != m.routing.available {
		m.routing = routingUI{available: available, epoch: epoch}
		m.groupsFresh = false
		for id := range m.pending {
			if id.Group == "GLOBAL" {
				delete(m.pending, id)
			}
		}
	}
}

// SetRouting observes authoritative routing state for this daemon session.
func (m *Model) SetRouting(status protocol.RoutingStatus, epoch uint64) {
	m.autoDirty = true
	if !m.routing.available || epoch != m.routing.epoch || (m.routing.known && status.Revision < m.routing.status.Revision) {
		return
	}
	if status.SubscriptionID != m.routing.status.SubscriptionID {
		for id := range m.pending {
			if id.Group == "GLOBAL" {
				delete(m.pending, id)
			}
		}
	}
	m.routing.status, m.routing.known = status, true
}

// RoutingUnavailable invalidates live data without clearing saved display values.
func (m *Model) RoutingUnavailable(epoch uint64) {
	if epoch != m.routing.epoch {
		return
	}
	m.routing.known = false
	m.routing.err = "Routing status unavailable; waiting for refresh"
}

// InvalidateGroups prevents a retained candidate list from authorizing a selection.
func (m *Model) InvalidateGroups() { m.groupsFresh = false }

func (m *Model) HelpMode() string {
	if m.routing.open {
		return ui.ModeRouting
	}
	return ""
}

// FooterHints describes the action available at the current control or overlay.
func (m *Model) FooterHints() string {
	if m.routing.open {
		return ui.RenderFooter(m.ID(), ui.ModeRouting, ui.FooterOpt{})
	}
	if m.routing.available && m.routing.focus >= 0 {
		return "↑/↓/←/→ move  Enter select  F4 settings  Esc back  ? help  q quit"
	}
	if m.focus.Locate {
		return "↑/↓ move  ← group  Enter locate  Esc back  ? help  q quit"
	}
	return ui.RenderFooter(m.ID(), "", ui.FooterOpt{})
}

// routingHeader keeps status explanations visible and adds optional action hints
// only beside a focused value when the complete hint fits.
func (m *Model) routingHeader() []string {
	if !m.routing.available {
		return nil
	}
	status := m.routing.status
	mode := "Loading…"
	if status.DesiredMode != "" {
		mode = routingLabel(status.DesiredMode)
	}
	note := ""
	if status.State == "pending" {
		note = "Saved · pending"
	} else if !m.routing.known || status.State == "unknown" {
		note = "Live state unavailable"
	}
	global := status.GlobalSelection
	switch global {
	case "":
		global = "Not selected"
	case "DIRECT":
		global = "DIRECT · direct connection"
	}
	values := []string{mode, ui.DisplayProxyName(global)}
	labels := []string{"Mode", "GLOBAL"}
	notes := []string{note, ""}
	actions := []string{" · Press Enter to Change", " · Press Enter to Select"}
	if !m.globalCandidatesCurrent() {
		notes[1] = "Waiting for candidates"
	}
	inner := ui.FullSectionInner(m.width)
	textWidth := ui.SectionTextWidth(inner)
	lines := make([]string, 0, 2)
	for i, label := range labels {
		rowWidth := textWidth
		if i == 0 {
			rowWidth = max(15, min(textWidth/2, textWidth-17))
			need := 11 + lipgloss.Width(values[i])
			if notes[i] != "" {
				need += 1 + lipgloss.Width(notes[i])
			} else if m.routing.focus == i && m.contentFocused {
				need += lipgloss.Width(actions[i])
			}
			if notes[i] != "" {
				rowWidth = max(rowWidth, min(need, textWidth-17))
			} else if need <= textWidth-17 {
				rowWidth = max(rowWidth, need)
			}
		}
		prefix := "  "
		focused := m.routing.focus == i && m.contentFocused
		if focused {
			prefix = ui.FocusMarker
		}
		row := prefix + routingLabelStyle.Render(label)
		valueWidth := max(0, rowWidth-lipgloss.Width(row))
		latency := ""
		if i == 1 {
			latency = m.extraLatency(status.GlobalSelection)
		}
		if lipgloss.Width(latency)+1 > valueWidth {
			latency = ""
		}
		valueWidth -= lipgloss.Width(latency)
		suffix := ""
		if notes[i] != "" {
			// Status explanations remain visible without focus and retain their
			// existing width priority and right alignment.
			note := ui.TruncateVisible(notes[i], max(0, valueWidth-min(9, lipgloss.Width(values[i]))-1))
			valueWidth = max(0, valueWidth-lipgloss.Width(note)-1)
			value := ui.TruncateVisible(values[i], valueWidth)
			padding := max(1, rowWidth-lipgloss.Width(row)-lipgloss.Width(value)-lipgloss.Width(note)-lipgloss.Width(latency))
			suffix = strings.Repeat(" ", padding) + m.theme.Muted.Render(note)
		} else if focused && lipgloss.Width(values[i]+actions[i]) <= valueWidth {
			// Optional actions never take space from the value or appear partially.
			suffix = m.theme.Muted.Render(actions[i])
		}
		row += m.theme.Success.Render(ui.TruncateVisible(values[i], valueWidth))
		if focused {
			row = ui.ApplyFocusStyle(row, m.theme.RowFocus)
		}
		// Keep both action hints and status explanations outside reverse video.
		line := ui.TruncateVisible(row+latency+suffix, rowWidth)
		if i == 0 {
			settings := "  Page Settings"
			if m.routing.focus == 2 && m.contentFocused {
				settings = m.theme.RowFocus.Render("› Page Settings")
			}
			line = ui.PadCell(line, rowWidth, ui.AlignLeft) + "  " + settings
		}
		lines = append(lines, ui.TruncateVisible(line, textWidth))
	}
	if status.Message != "" {
		lines = append(lines, m.theme.Muted.Render(ui.TruncateVisible("  "+status.Message, textWidth)))
	}
	return strings.Split(ui.RenderBorderedSection(m.theme, "Basic", strings.Join(lines, "\n"), inner), "\n")
}

func routingLabel(mode string) string {
	for i, value := range routingModes {
		if value == mode {
			return routingLabels[i]
		}
	}
	return "Unknown"
}

func (m *Model) routingKey(key string) (bool, tea.Cmd) {
	if !m.routing.available {
		return false, nil
	}
	if m.routing.open {
		if m.routing.pending {
			return true, nil
		}
		switch key {
		case "esc":
			m.routing.open = false
			m.routing.err = ""
		case "up":
			m.routing.cursor = (m.routing.cursor + 2) % 3
		case "down":
			m.routing.cursor = (m.routing.cursor + 1) % 3
		case "enter":
			return true, m.submitRouting()
		}
		return true, nil
	}
	if m.routing.focus < 0 {
		return false, nil
	}
	switch key {
	case "esc":
		return true, func() tea.Msg { return ui.FocusRailMsg{} }
	case "pgdown":
		if len(m.groups) > 0 {
			m.routing.focus = -1
			m.focus = FocusID{Group: m.groups[0].Name}
			m.movePage(1)
		}
	case "right":
		m.routing.focus = 2
	case "left":
		if m.routing.focus == 2 {
			m.routing.focus = 0
		}
	case "tab":
		switch m.routing.focus {
		case 0:
			m.routing.focus = 2
		case 2:
			m.routing.focus = 1
		case 1:
			m.routing.focus = 0
		}
	case "shift+tab":
		switch m.routing.focus {
		case 0:
			m.routing.focus = 1
		case 1:
			m.routing.focus = 2
		case 2:
			m.routing.focus = 0
		}
	case "up":
		m.routing.focus = 0
	case "down":
		if m.routing.focus == 0 || m.routing.focus == 2 {
			m.routing.focus = 1
		} else if len(m.groups) > 0 {
			m.routing.focus = -1
			m.focus = FocusID{Group: m.groups[0].Name}
			m.ensureFocusVisible()
		}
	case "enter":
		if m.routing.focus == 2 {
			return true, func() tea.Msg { return ui.OpenPageSettingsMsg{Page: ui.PageProxies} }
		}
		if m.routing.focus == 0 {
			if !m.routing.known {
				m.lastError = "Routing status unavailable; wait for refresh"
				return true, nil
			}
			m.routing.open = true
			m.routing.err = ""
			for i, mode := range routingModes {
				if mode == m.routing.status.DesiredMode {
					m.routing.cursor = i
				}
			}
		} else if m.globalCandidatesCurrent() {
			if index := m.groupIndex("GLOBAL"); index >= 0 {
				m.routing.focus = -1
				m.expanded["GLOBAL"] = true
				m.focus = FocusID{Group: "GLOBAL"}
				// Reveal all candidates when they fit; an oversized section
				// starts at the viewport top instead of leaving only its header visible.
				lines, start, end := m.buildContent(true)
				m.scrollY = ui.EnsureLineVisible(m.scrollY, max(1, m.height-len(m.routingHeader())), len(lines), start, end)
			}
		}
	}
	return true, nil
}

func (m *Model) globalCandidatesCurrent() bool {
	return m.groupsFresh && m.groupsRevision != nil && m.routing.known && *m.groupsRevision == m.routing.status.Revision && m.groupsSubscription == m.routing.status.SubscriptionID && m.routing.status.State != "pending"
}

func (m *Model) submitRouting() tea.Cmd {
	client, ok := m.client.(routingClient)
	if !ok || !m.routing.known {
		m.routing.err = "Routing mode is unavailable"
		return nil
	}
	mode := routingModes[m.routing.cursor]
	if mode == m.routing.status.DesiredMode && m.routing.status.State == "applied" {
		m.routing.open = false
		return nil
	}
	id, epoch := m.newOperationID(), m.routing.epoch
	revision := m.routing.status.Revision
	m.routing.pending = true
	m.routing.err = ""
	execute := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		status, err := client.UpdateRouting(ctx, protocol.RoutingUpdateRequest{OperationID: id, IfRevision: &revision, Mode: mode})
		return routingResultMsg{status: status, epoch: epoch, err: err}
	}
	return func() tea.Msg {
		return ui.ActionIntentMsg{Action: ui.ActionSetRouting, Page: ui.PageProxies, Capability: protocol.CapabilityRouting, Key: "routing", Title: "Change routing mode", Object: routingLabel(mode), Execute: execute, Cancel: func() tea.Msg {
			return ui.PageResultMsg{Page: ui.PageProxies, Result: routingResultMsg{epoch: epoch, cancelled: true}}
		}}
	}
}

func (m *Model) routingResult(result routingResultMsg) {
	if result.epoch != m.routing.epoch {
		return
	}
	m.routing.pending = false
	if result.cancelled {
		return
	}
	if result.err != nil {
		m.routing.known = false
		m.routing.err = "Could not confirm mode; wait for refresh"
		var api protocol.APIError
		if errors.As(result.err, &api) && api.Code == protocol.CodeRevisionConflict {
			m.routing.err = "State changed; refresh and try again"
		}
		return
	}
	m.SetRouting(result.status, result.epoch)
	m.routing.open = false
	m.routing.err = ""
	m.routing.focus = 0
}

func (m *Model) routingView() string {
	width := min(54, max(24, m.width-8))
	textWidth := max(16, width-6)
	lines := []string{m.theme.Title.Render("Routing Mode"), m.theme.Muted.Render("Choose how traffic leaves mihomo"), ""}
	for i, label := range routingLabels {
		marker := "  "
		style := m.theme.Button
		if m.routing.cursor == i {
			marker = "› "
			style = m.theme.RowFocus
		}
		note := ""
		if m.routing.status.LiveMode == routingModes[i] && m.routing.status.State != "pending" {
			note = "  Current"
		} else if m.routing.status.DesiredMode == routingModes[i] {
			note = "  Saved"
		}
		lines = append(lines, style.Width(textWidth).Render(marker+label+note), m.theme.Muted.Render("  "+routingDescriptions[i]), "")
	}
	if m.routing.pending {
		lines = append(lines, m.theme.Info.Render("Applying…"))
	} else if m.routing.err != "" {
		lines = append(lines, m.theme.Danger.Width(textWidth).Render(m.routing.err))
	} else {
		lines = append(lines, m.theme.Muted.Render("↑↓ Select   Enter Apply   Esc Cancel"))
	}
	box := m.theme.Dialog.Width(width).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
