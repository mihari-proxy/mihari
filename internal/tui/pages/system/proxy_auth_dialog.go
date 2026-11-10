package system

import (
	"strings"
	"time"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// authViewportWidth excludes the shell's content padding from the overlay area.
func (m *Model) authViewportWidth() int {
	return max(1, m.width-m.theme.Content.GetHorizontalFrameSize())
}

func (m *Model) authView() string {
	outer := max(12, min(144, m.authViewportWidth()-4))
	inner := max(6, outer-m.theme.Dialog.GetHorizontalFrameSize())
	wide := inner >= 112
	leftWidth, rightWidth := inner, inner
	if wide {
		leftWidth = (inner - 3) / 2
		rightWidth = inner - 3 - leftWidth
	}
	hint := "↑/↓ select · Enter open/edit · d remove · Tab pane · Ctrl+R reveal"
	if m.auth.edit != "" {
		hint = "Enter accept · Tab field · Ctrl+R reveal"
	}
	if m.auth.picker != "" {
		hint = "↑/↓ choose mode · Enter confirm · Esc close options"
	}
	hints := strings.Split(ansi.Wrap(hint, inner, ""), "\n")
	errors := m.authErrorLines(inner)
	available := max(2, m.height-m.theme.Dialog.GetVerticalFrameSize()-len(hints)-len(errors)-6)
	parameters := m.authParameterContent(leftWidth)
	natural := len(parameters)
	previewNatural := len(m.auth.preview.Lines) + 8
	leftHeight, rightHeight := 0, 0
	if wide {
		budget := min(available, max(natural, min(24, previewNatural)))
		leftHeight, rightHeight = budget, budget
	} else {
		budget := min(available, natural+previewNatural+1)
		leftHeight = max(1, budget/2)
		rightHeight = max(1, budget-leftHeight-1)
	}
	left := m.authParameterWindow(parameters, leftWidth, leftHeight)
	right := m.authPreviewLines(rightWidth, rightHeight)
	var body string
	if wide {
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(leftWidth).Height(leftHeight).Render(strings.Join(left, "\n")),
			m.theme.Muted.Render(strings.TrimSuffix(strings.Repeat(" │ \n", leftHeight), "\n")),
			lipgloss.NewStyle().Width(rightWidth).Render(strings.Join(right, "\n")))
	} else {
		body = strings.Join(left, "\n") + "\n" + m.theme.Muted.Render(strings.Repeat("─", inner)) + "\n" + strings.Join(right, "\n")
	}
	rule := m.theme.Muted.Render(strings.Repeat("─", inner))
	dirty := "Unsaved changes"
	if !m.authDirty() {
		dirty = "No changes"
	}
	title := m.theme.Title.Render("Proxy Authentication")
	feedback := m.theme.Muted.Render(dirty)
	if m.auth.err != "" {
		feedback = ui.RenderStatusChip(m.theme, ui.StatusChipFailed, ui.FailedLabel)
	}
	header := title + strings.Repeat(" ", max(1, inner-ansi.StringWidth(title)-ansi.StringWidth(feedback))) + feedback
	n := len(m.authUsers())
	resetRow := 5 + n + len(m.auth.draft.BypassPrefixes)
	reset, save, cancel := "[ Reset authentication ]", "[Save]", "[Cancel]"
	if m.auth.selected == resetRow {
		reset = m.theme.RowSelected.Render(reset)
	}
	if m.auth.selected == resetRow+1 {
		save = m.theme.RowSelected.Render(save)
	}
	if m.auth.selected == resetRow+2 {
		cancel = m.theme.RowSelected.Render(cancel)
	}
	saveHint := m.theme.Muted.Render("Ctrl+S")
	if m.auth.pending {
		clock := m.rowSpinClock
		if clock.IsZero() {
			clock = time.Unix(0, 0)
		}
		saveHint = ui.RenderStatusChip(m.theme, ui.StatusChipPending, ui.SpinnerLabel(clock, "Saving"))
	} else if m.auth.saveDone && !m.authDirty() {
		saveHint = ui.RenderStatusChip(m.theme, ui.StatusChipDone, ui.DoneLabel)
	}
	actions := cancel + " " + m.theme.Muted.Render("Esc") + "  " + save + " " + saveHint
	lines := append([]string{header}, errors...)
	lines = append(lines, rule, body, rule, reset, actions, m.theme.Muted.Render(strings.Join(hints, "\n")))
	// Very small viewports reserve the frame first; all parameters and YAML remain
	// available through their independent scrolling panes.
	dialog := m.theme.Dialog.Width(outer).Render(strings.Join(ui.SliceLines(strings.Split(ansi.Hardwrap(strings.Join(lines, "\n"), inner, true), "\n"), 0, max(1, m.height-m.theme.Dialog.GetVerticalFrameSize())), "\n"))
	if m.auth.exitOpen {
		return ui.CenterOverlay(m.theme, dialog, m.authExitView(inner), lipgloss.Width(dialog), lipgloss.Height(dialog))
	}
	return dialog
}
