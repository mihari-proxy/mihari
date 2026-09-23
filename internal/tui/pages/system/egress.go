package system

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
	top                   int
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
	return "No-Override"
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
		if result.status.Selection.Mode == "manual" {
			m.egress.candidate = result.status.Selection.InterfaceName
		} else {
			m.egress.candidate = ""
		}
		return func() tea.Msg { return ui.RuntimeRevisionMsg{Revision: result.status.Revision} }
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

func (m *Model) egressMatchesSelection() bool {
	selection := m.egress.status.Selection
	if m.egress.candidate == "" {
		return selection.Mode == "automatic"
	}
	return selection.Mode == "manual" && selection.InterfaceName == m.egress.candidate
}

func (m *Model) egressCandidateSelectable() bool {
	if m.egress.candidate == "" {
		return true
	}
	for _, item := range m.egress.status.Interfaces {
		if item.Name == m.egress.candidate && item.Selectable {
			return true
		}
	}
	return false
}

func (m *Model) updateEgressDialog(message tea.Msg) tea.Cmd {
	key, ok := message.(tea.KeyPressMsg)
	if !ok || m.egress.pending {
		return nil
	}
	switch key.String() {
	case "esc":
		return m.closeEgressDialog()
	case "tab", "shift+tab":
		return nil
	case "pgup":
		m.egress.detailTop = max(0, m.egress.detailTop-4)
	case "pgdown":
		m.egress.detailTop += 4
	case "up", "down":
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
		if m.egressMatchesSelection() {
			return m.closeEgressDialog()
		}
		if !m.egressCandidateSelectable() || !m.mutationsEnabled || !m.egress.loaded {
			return nil
		}
		return m.applyEgress()
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

func (m *Model) egressAvailabilityStyle(state string) lipgloss.Style {
	switch state {
	case "available":
		return m.theme.Info
	case "disconnected":
		return m.theme.Warning
	case "not_found":
		return m.theme.Danger
	default:
		return m.theme.Muted
	}
}

func (m *Model) egressValueStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
}

func (m *Model) egressInterfaceLines(item *protocol.EgressInterface, width int) []string {
	automatic := item == nil
	name := "No-Override"
	if !automatic {
		name = item.Name
	}
	chosen := automatic && m.egress.candidate == "" || !automatic && name == m.egress.candidate
	selection := m.egress.status.Selection
	effective := automatic && selection.Mode == "automatic" || !automatic && selection.Mode == "manual" && selection.InterfaceName == name
	const prefixWidth = 4
	nameWidth := max(1, width-prefixWidth)
	parts := wrapColumns(diagnostics.EscapeTerminal(name), nameWidth)
	gutter := "  "
	if effective {
		gutter = m.theme.BrightYellow.Render("▸") + " "
	}
	dot := "  "
	if !automatic {
		dot = m.egressAvailabilityStyle(item.Availability).Render("●") + " "
	}
	indent := strings.Repeat(" ", prefixWidth)
	lines := make([]string, 0, len(parts))
	for i, part := range parts {
		text := part
		if !chosen && effective {
			text = m.theme.BrightYellow.Render(part)
		} else if !chosen && !automatic && !item.Selectable {
			text = m.theme.Muted.Render(part)
		}
		if chosen {
			text = ui.ApplyFocusStyle(ui.PadCell(text, nameWidth, ui.AlignLeft), m.theme.RowFocus)
		}
		if i == 0 {
			lines = append(lines, gutter+dot+text)
			continue
		}
		lines = append(lines, indent+text)
	}
	return lines
}

func wrapColumns(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := ""
	flush := func() {
		if current == "" {
			return
		}
		lines = append(lines, current)
		current = ""
	}
	for _, word := range words {
		if ansi.StringWidth(word) > width {
			flush()
			lines = append(lines, strings.Split(ansi.Hardwrap(word, width, false), "\n")...)
			continue
		}
		if current == "" {
			current = word
			continue
		}
		if next := current + " " + word; ansi.StringWidth(next) <= width {
			current = next
			continue
		}
		flush()
		current = word
	}
	flush()
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func egressWindowStart(counts []int, selected, budget int) int {
	if selected < 0 || len(counts) == 0 || budget < 1 {
		return 0
	}
	start := selected
	used := counts[selected]
	if used > budget {
		return selected
	}
	for start > 0 && used+counts[start-1] <= budget {
		start--
		used += counts[start]
	}
	return start
}

func (m *Model) egressDetailLines(candidate *protocol.EgressInterface, width int) []string {
	const labelCol = 12
	white := m.egressValueStyle()
	lines := []string{m.theme.Title.Render(ui.TruncateVisible("● DETAILS", width))}
	add := func(label, value string, style lipgloss.Style) {
		gap := labelCol - ansi.StringWidth(label)
		if gap < 2 {
			gap = 2
		}
		column := ansi.StringWidth(label) + gap
		valueWidth := max(1, width-column)
		parts := wrapColumns(diagnostics.EscapeTerminal(value), valueWidth)
		for i, part := range parts {
			if i == 0 {
				lines = append(lines, m.theme.Muted.Render(label)+strings.Repeat(" ", gap)+style.Render(part))
				continue
			}
			lines = append(lines, strings.Repeat(" ", column)+style.Render(part))
		}
	}
	cont := func(value string, style lipgloss.Style) {
		parts := wrapColumns(diagnostics.EscapeTerminal(value), max(1, width-labelCol))
		for _, part := range parts {
			lines = append(lines, strings.Repeat(" ", labelCol)+style.Render(part))
		}
	}
	if m.egress.candidate == "" {
		add("Name", "No-Override", white)
		cont("Use existing configuration", white)
		return lines
	}
	if candidate == nil {
		add("Name", m.egress.candidate, white)
		add("Status", "No longer selectable", white)
		return lines
	}
	add("Name", candidate.Name, white)
	if candidate.Device != "" {
		add("Device", candidate.Device, white)
	}
	add("Status", egressAvailability(candidate.Availability), m.egressAvailabilityStyle(candidate.Availability))
	kind := candidate.Kind
	if kind == "" {
		kind = ui.MissingValue
	}
	add("Type", kind, white)
	if len(candidate.Addresses) == 0 {
		add("Addresses", ui.MissingValue, white)
	} else {
		add("Addresses", candidate.Addresses[0], white)
		for _, address := range candidate.Addresses[1:] {
			cont(address, white)
		}
	}
	if !candidate.Selectable {
		add("Reason", candidate.Reason, white)
	}
	return lines
}

func (m *Model) egressFootNote() string {
	note := "Enter applies and closes active connections. Esc closes without changes."
	switch m.egress.status.State {
	case "unknown":
		note = "Saved configuration; application unconfirmed."
	case "saved":
		note = "Save for the next core start."
	}
	if m.egress.err != "" {
		note = m.egress.err
	}
	if m.egress.pending {
		note = "Applying…"
	}
	return note
}

// egressBoxWidth matches the subscription dialog offset: stay 8 columns inside
// the page, and stop growing once the box is wide enough for both columns.
func (m *Model) egressBoxWidth() int {
	return min(100, max(40, m.layoutWidth()-8))
}

func (m *Model) egressDialogView() string {
	frameWidth := m.theme.Dialog.GetHorizontalFrameSize()
	width := m.egressBoxWidth()
	inner := max(12, width-frameWidth)
	wide := inner >= 58
	frame, fixed := 4, 2
	if wide && m.height >= 28 {
		fixed = 3
	}
	noteLines := wrapColumns(diagnostics.EscapeTerminal(m.egressFootNote()), inner)
	if len(noteLines) == 0 {
		noteLines = []string{""}
	}
	budget := max(6, m.height-frame-fixed-max(0, len(noteLines)-1))
	leftWidth := inner
	if wide {
		usable := inner - 3
		leftWidth = max(28, usable/2)
		if usable-leftWidth < 28 {
			leftWidth = max(12, usable-28)
		}
	}
	detailLines := 5
	noOverride := m.egressInterfaceLines(nil, leftWidth)
	rendered := make([][]string, len(m.egress.status.Interfaces))
	counts := make([]int, len(rendered))
	for i := range m.egress.status.Interfaces {
		item := m.egress.status.Interfaces[i]
		rendered[i] = m.egressInterfaceLines(&item, leftWidth)
		counts[i] = max(1, len(rendered[i]))
	}
	detailReserve := 0
	if !wide {
		detailReserve = 1 + detailLines
	}
	ifaceBudget := max(1, budget-1-len(noOverride)-detailReserve)
	selected := m.egressCandidateIndex() - 1
	if selected >= 0 {
		m.egress.top = egressWindowStart(counts, selected, ifaceBudget)
	}
	if len(rendered) == 0 {
		m.egress.top = 0
	} else {
		m.egress.top = max(0, min(m.egress.top, len(rendered)-1))
	}

	var candidate *protocol.EgressInterface
	for i := range m.egress.status.Interfaces {
		if m.egress.status.Interfaces[i].Name == m.egress.candidate {
			item := m.egress.status.Interfaces[i]
			candidate = &item
			break
		}
	}
	left := []string{m.theme.Title.Render(ui.TruncateVisible("● INTERFACES", leftWidth))}
	left = append(left, noOverride...)
	used := 0
	for i := m.egress.top; i < len(rendered); i++ {
		if used > 0 && used+counts[i] > ifaceBudget {
			break
		}
		take := rendered[i]
		if used == 0 && len(take) > ifaceBudget {
			take = take[:ifaceBudget]
		}
		left = append(left, take...)
		used += len(take)
		if used >= ifaceBudget {
			break
		}
	}
	rightWidth := inner
	detailHeight := detailLines + 1
	if wide {
		rightWidth = inner - leftWidth - 3
		detailHeight = len(left)
	}
	details := m.egressDetailLines(candidate, rightWidth)
	m.egress.detailTop = max(0, min(m.egress.detailTop, max(0, len(details)-detailHeight)))
	visible := ui.SliceLines(details, m.egress.detailTop, detailHeight)
	body := strings.Join(left, "\n")
	if wide {
		for len(visible) < len(left) {
			visible = append(visible, "")
		}
		rule := make([]string, len(left))
		for i := range rule {
			rule[i] = " │ "
		}
		right := lipgloss.NewStyle().Width(rightWidth).Render(strings.Join(visible, "\n"))
		body = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(leftWidth).Render(body), strings.Join(rule, "\n"), right)
	} else {
		body += "\n" + strings.Join(visible, "\n")
	}
	content := m.theme.Title.Render("Outbound Interface Override")
	if fixed == 3 {
		content += "\n"
	}
	content += "\n" + body + "\n" + strings.Join(noteLines, "\n")
	return m.theme.Dialog.Width(width).Render(content)
}
