package system

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type egressClient interface {
	Egress(context.Context) (protocol.EgressStatus, error)
	UpdateEgress(context.Context, protocol.EgressUpdateRequest) (protocol.EgressStatus, error)
}

type egressUI struct {
	status                protocol.EgressStatus
	loaded, open, pending bool
	epoch, revision       uint64
	candidate             string
	detailTop             int
	focus, top            int // list, Cancel, Apply
	err                   string
}

type egressResultMsg struct {
	status   protocol.EgressStatus
	epoch    uint64
	mutation bool
	err      error
}

func (m egressResultMsg) Err() error                        { return m.err }
func (m egressResultMsg) Warnings() protocol.WarningOutcome { return m.status.WarningOutcome }

func egressLabel(selection protocol.EgressSelection) string {
	if selection.Mode == "manual" {
		return selection.InterfaceName
	}
	return "Automatic"
}
func egressAvailability(state string) string {
	switch state {
	case "available":
		return "Available"
	case "disconnected":
		return "Disconnected"
	case "not_found":
		return "Not found"
	default:
		return "Unknown"
	}
}

// HasEgressDialog lets the shell keep keyboard ownership synchronous with the dialog.
func (m *Model) HasEgressDialog() bool { return m.egress.open }

// SetEgress observes a daemon snapshot without discarding a dialog's draft or revision.
func (m *Model) SetEgress(status protocol.EgressStatus) {
	if m.egress.loaded && status.Revision < m.egress.status.Revision {
		return
	}
	m.egress.status, m.egress.loaded = status, true
	if !m.egress.open {
		m.egress.candidate = status.Selection.InterfaceName
	}
}

func (m *Model) loadEgress() tea.Cmd {
	client, ok := m.client.(egressClient)
	if !ok {
		return nil
	}
	epoch, ctx := m.egress.epoch, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		status, err := client.Egress(ctx)
		return ui.PageResultMsg{Page: ui.PageSystem, Result: egressResultMsg{status: status, epoch: epoch, err: err}}
	}
}

func (m *Model) openEgressDialog() tea.Cmd {
	if !m.mutationsEnabled || !m.hasCapability(protocol.CapabilityEgress) {
		return nil
	}
	if _, ok := m.client.(egressClient); !ok {
		return nil
	}
	m.egress.open = true
	m.egress.epoch++
	m.egress.focus = 0
	m.egress.top = 0
	m.egress.detailTop = 0
	m.egress.err = ""
	m.egress.candidate = m.egress.status.Selection.InterfaceName
	m.egress.revision = m.egress.status.Revision
	return tea.Batch(func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputText} }, m.loadEgress())
}

func (m *Model) handleEgressResult(result egressResultMsg) tea.Cmd {
	if result.epoch != m.egress.epoch {
		return nil
	}
	if result.mutation {
		m.egress.pending = false
		if result.err != nil {
			m.egress.err = actionErrorDetail(result.err, "Could not apply outbound interface") + " (F2: details)"
			return m.loadEgress()
		}
		m.SetEgress(result.status)
		m.egress.open = false
		m.egress.epoch++
		return tea.Batch(func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }, func() tea.Msg { return ui.RuntimeRevisionMsg{Revision: result.status.Revision} })
	}
	if result.err != nil {
		m.egress.err = actionErrorDetail(result.err, "Could not read network interfaces") + " (F2: details)"
		return nil
	}
	wasLoaded := m.egress.loaded
	m.SetEgress(result.status)
	if m.egress.open && (!wasLoaded || m.egress.err != "") {
		m.egress.revision = result.status.Revision
		if !wasLoaded {
			m.egress.candidate = result.status.Selection.InterfaceName
		}
	}
	return nil
}

func (m *Model) egressCandidateIndex() int {
	for i, item := range m.egress.status.Interfaces {
		if item.Name == m.egress.candidate {
			return i + 1
		}
	}
	return 0
}

func (m *Model) egressChanged() bool {
	return m.egress.candidate != m.egress.status.Selection.InterfaceName
}

func (m *Model) updateEgressDialog(message tea.Msg) tea.Cmd {
	key, ok := message.(tea.KeyPressMsg)
	if !ok || m.egress.pending {
		return nil
	}
	switch key.String() {
	case "esc":
		return m.closeEgressDialog()
	case "tab":
		m.egress.focus = (m.egress.focus + 1) % 3
	case "shift+tab":
		m.egress.focus = (m.egress.focus + 2) % 3
	case "pgup":
		m.egress.detailTop = max(0, m.egress.detailTop-4)
	case "pgdown":
		m.egress.detailTop += 4
	case "up", "down":
		if m.egress.focus != 0 {
			return nil
		}
		m.egress.detailTop = 0
		delta := 1
		if key.String() == "up" {
			delta = -1
		}
		for next := m.egressCandidateIndex() + delta; next >= 0 && next <= len(m.egress.status.Interfaces); next += delta {
			if next == 0 {
				m.egress.candidate = ""
				break
			}
			item := m.egress.status.Interfaces[next-1]
			if item.Selectable {
				m.egress.candidate = item.Name
				break
			}
		}
	case "enter", "space":
		if m.egress.focus == 1 {
			return m.closeEgressDialog()
		}
		if m.egress.focus == 2 && m.egressChanged() && m.mutationsEnabled && m.egress.loaded {
			return m.applyEgress()
		}
	}
	return nil
}

func (m *Model) closeEgressDialog() tea.Cmd {
	m.egress.open = false
	m.egress.epoch++
	m.egress.err = ""
	return func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
}

func (m *Model) applyEgress() tea.Cmd {
	client, ok := m.client.(egressClient)
	if !ok {
		return nil
	}
	selection := m.egress.candidate
	if selection != "" {
		valid := false
		for _, item := range m.egress.status.Interfaces {
			if item.Name == selection && item.Selectable {
				valid = true
			}
		}
		if !valid {
			m.egress.err = "Interface is no longer selectable"
			return nil
		}
	}
	id := m.newOperationID()
	revision, epoch := m.egress.revision, m.egress.epoch
	request := protocol.EgressUpdateRequest{OperationID: id, IfRevision: &revision, Mode: "automatic"}
	if selection != "" {
		request.Mode = "manual"
		request.InterfaceName = selection
	}
	m.egress.pending = true
	m.egress.err = ""
	ctx := m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		status, err := client.UpdateEgress(ctx, request)
		return ui.PageResultMsg{Page: ui.PageSystem, Result: egressResultMsg{status: status, epoch: epoch, mutation: true, err: err}}
	}
}

func (m *Model) egressDialogView() string {
	width := max(18, min(82, m.width-4))
	inner := max(12, width-6)
	wide := inner >= 58
	bodyHeight := max(2, m.height-15)
	if !wide {
		bodyHeight = max(2, bodyHeight-4)
	}
	slots := max(1, bodyHeight/2)
	selected := m.egressCandidateIndex() - 1
	if selected >= 0 {
		if selected < m.egress.top {
			m.egress.top = selected
		}
		if selected >= m.egress.top+slots {
			m.egress.top = selected - slots + 1
		}
	}
	m.egress.top = max(0, min(m.egress.top, max(0, len(m.egress.status.Interfaces)-slots)))
	leftWidth := inner
	if wide {
		leftWidth = (inner - 3) / 2
	}
	line := func(text string) string { return ui.TruncateVisible(diagnostics.EscapeTerminal(text), leftWidth) }
	selectionRow := func(name, note string, chosen, saved, enabled bool) string {
		marker := "  "
		if chosen {
			marker = "› "
		}
		label := marker + name
		if saved {
			label += " · Saved"
		}
		a, b := line(label), line("  "+note)
		if !enabled {
			a = m.theme.Muted.Render(a)
		}
		if chosen && m.egress.focus == 0 {
			a = ui.ApplyFocusStyle(ui.PadCell(a, leftWidth, ui.AlignLeft), m.theme.RowFocus)
			b = ui.ApplyFocusStyle(ui.PadCell(b, leftWidth, ui.AlignLeft), m.theme.RowFocus)
		}
		return a + "\n" + b
	}
	left := []string{m.theme.Muted.Render("INTERFACES  ↑↓"), selectionRow("Automatic", "Use existing configuration", m.egress.candidate == "", m.egress.status.Selection.Mode == "automatic", true), m.theme.Muted.Render(strings.Repeat("─", leftWidth))}
	var candidate *protocol.EgressInterface
	for i, item := range m.egress.status.Interfaces {
		if item.Name == m.egress.candidate {
			copy := item
			candidate = &copy
		}
		if i < m.egress.top || i >= m.egress.top+slots {
			continue
		}
		note := egressAvailability(item.Availability)
		if !item.Selectable {
			note = item.Reason
		} else if item.Kind != "unknown" && item.Kind != "" {
			note = item.Kind + " · " + note
		}
		left = append(left, selectionRow(item.Name, note, item.Name == m.egress.candidate, item.Name == m.egress.status.Selection.InterfaceName, item.Selectable))
	}
	left = append(left, m.theme.Muted.Render(fmt.Sprintf("%d–%d / %d", min(len(m.egress.status.Interfaces), m.egress.top+1), min(len(m.egress.status.Interfaces), m.egress.top+slots), len(m.egress.status.Interfaces))))
	details := []string{m.theme.Muted.Render("DETAILS"), "Automatic", "Use existing configuration"}
	if m.egress.candidate != "" && candidate == nil {
		details = []string{"DETAILS", diagnostics.EscapeTerminal(m.egress.candidate), "No longer selectable"}
	}
	if candidate != nil {
		details = []string{m.theme.Muted.Render("DETAILS"), diagnostics.EscapeTerminal(candidate.Name), "Status  " + egressAvailability(candidate.Availability), "Type    " + candidate.Kind, "Addresses"}
		for _, address := range candidate.Addresses {
			details = append(details, diagnostics.EscapeTerminal(address))
		}
		if len(candidate.Addresses) == 0 {
			details = append(details, "—")
		}
	}
	if m.egressChanged() {
		details = append(details, m.theme.Info.Render("Not applied"))
	} else {
		details = append(details, m.theme.Muted.Render("Saved selection"))
	}
	body := strings.Join(left, "\n")
	rightWidth, detailHeight := inner, 4
	if wide {
		rightWidth, detailHeight = inner-leftWidth-3, lipgloss.Height(body)
	}
	wrapped := strings.Split(lipgloss.NewStyle().Width(rightWidth).Render(strings.Join(details, "\n")), "\n")
	m.egress.detailTop = max(0, min(m.egress.detailTop, max(0, len(wrapped)-detailHeight)))
	visibleDetails := strings.Join(ui.SliceLines(wrapped, m.egress.detailTop, detailHeight), "\n")
	if wide {
		right := lipgloss.NewStyle().Width(rightWidth).Render(visibleDetails)
		body = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(leftWidth).Render(body), " \u2502 ", right)
	} else {
		body += "\n" + visibleDetails
	}

	change := "No changes to apply."
	if m.egressChanged() {
		name := m.egress.candidate
		if name == "" {
			name = "Automatic"
		}
		change = "Apply " + egressLabel(m.egress.status.Selection) + " → " + name
	}
	note := " "
	if m.egress.status.State == "unknown" {
		note = "Saved configuration; application unconfirmed."
	}
	if m.egressChanged() {
		note = "Applying will close active connections."
		if m.egress.status.State == "saved" {
			note = "Save for the next core start."
		}
	}
	if candidate != nil && candidate.Availability != "available" {
		note = "Interface unavailable. No automatic fallback."
	}
	if m.egress.err != "" {
		note = m.egress.err
	}
	if m.egress.pending {
		note = "Applying…"
	}
	button := func(label string, focus int) string {
		if m.egress.focus == focus {
			return m.theme.ButtonActive.Render(label)
		}
		return m.theme.Button.Render(label)
	}
	apply := button("[ Apply ]", 2)
	if !m.egressChanged() || m.egress.pending || !m.mutationsEnabled {
		apply = m.theme.Muted.Render("[ Apply ]")
	}
	savedLabel := egressLabel(m.egress.status.Selection)
	for _, item := range m.egress.status.Interfaces {
		if item.Name == m.egress.status.Selection.InterfaceName {
			savedLabel += " · " + egressAvailability(item.Availability)
		}
	}
	content := m.theme.Title.Render("Outbound Interface") + "\n" + m.theme.Muted.Render("System / Network") + "\n" + "Saved  " + ui.TruncateVisible(diagnostics.EscapeTerminal(savedLabel), inner-7) + "\n" + body + "\n" + m.theme.Muted.Render(strings.Repeat("─", inner)) + "\n" + ui.TruncateVisible(diagnostics.EscapeTerminal(change), inner) + "\n" + ui.TruncateVisible(diagnostics.EscapeTerminal(note), inner) + "\n" + button("[ Cancel ]", 1) + "  " + apply
	return m.theme.Dialog.Width(width).Render(content)
}
