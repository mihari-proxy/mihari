package tui

import (
	"fmt"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"github.com/mihari-proxy/mihari/internal/update"
)

func newMihariUpdateConfirmation(title, object string, content ui.MihariUpdateConfirmation) *Modal {
	content.Installed = append([]ui.MihariInstalledVersion(nil), content.Installed...)
	return &Modal{kind: modalMihariUpdate, title: title, object: object, selected: 1, updateContent: &content}
}

func (m *Modal) updateConfirmationView(theme ui.Theme, width, height int) string {
	if Classify(width, height) == ui.TooSmall {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
			theme.Title.Render(ui.ResizeRequired)+"\n"+theme.Muted.Render(ui.ResizeInstructions))
	}
	// Reserve the full frame before wrapping the body, and reserve
	// title/footer rows before slicing it.
	boxWidth := min(64, width-8)
	inner := max(1, boxWidth-theme.Dialog.GetHorizontalFrameSize())
	lines := updateConfirmationLines(theme, *m.updateContent, inner)
	visible := max(1, height-2-theme.Dialog.GetVerticalFrameSize()-6)
	visible = min(visible, len(lines))
	m.updateRows = visible
	m.scroll = min(m.scroll, max(0, len(lines)-visible))
	end := m.scroll + visible
	indicator := ""
	if len(lines) > visible {
		indicator = fmt.Sprintf("%s · %d–%d/%d", ui.UpdateScrollHint, m.scroll+1, end, len(lines))
	}
	confirm, cancel := theme.Button.Render(ui.ConfirmLabel), theme.Button.Render(ui.CancelLabel)
	if m.selected == 0 {
		confirm = theme.ButtonActive.Render(ui.ConfirmLabel)
	} else {
		cancel = theme.ButtonActive.Render(ui.CancelLabel)
	}
	buttons := lipgloss.NewStyle().Width(inner).Align(lipgloss.Right).Render(lipgloss.JoinHorizontal(lipgloss.Top, confirm, "  ", cancel))
	body := theme.Title.Render(m.title) + "\n\n" + strings.Join(lines[m.scroll:end], "\n") + "\n" +
		theme.Muted.Render(indicator) + "\n\n" + buttons + "\n" + theme.Muted.Render(ui.UpdateSelectHint)
	box := theme.Dialog.Width(boxWidth).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func updateConfirmationLines(theme ui.Theme, content ui.MihariUpdateConfirmation, width int) []string {
	heading := lipgloss.NewStyle().Bold(true)
	var blocks []string
	blocks = append(blocks, heading.Render(ui.UpdateInstalledHeading))
	roleWidth := 0
	for _, installed := range content.Installed {
		roleWidth = max(roleWidth, ansi.StringWidth(installed.Role))
	}
	for _, installed := range content.Installed {
		style := lipgloss.NewStyle()
		if installed.Unknown {
			style = theme.Warning
		}
		prefix := "  " + installed.Role + strings.Repeat(" ", roleWidth-ansi.StringWidth(installed.Role)+2)
		available := max(1, width-ansi.StringWidth(prefix))
		wrapped := strings.Split(ansi.Hardwrap(installed.Version, available, true), "\n")
		for n, line := range wrapped {
			label := strings.Repeat(" ", ansi.StringWidth(prefix))
			if n == 0 {
				label = theme.Muted.Render(prefix)
			}
			blocks = append(blocks, label+style.Render(line))
		}
	}
	blocks = append(blocks, theme.Muted.Render(ui.UpdateTargetHeading+"  ")+theme.Title.Render(content.TargetVersion))
	if content.Compatibility != "" {
		title, style := ui.UpdateCompatibilityUnknownHeading, theme.Warning
		if content.Risk == update.ReplacementDowngrade {
			title, style = ui.UpdateDowngradeHeading, theme.Danger
		}
		blocks = append(blocks, "", style.Bold(true).Render(title), ansi.Wrap(content.Compatibility, width, ""))
	}
	blocks = append(blocks, "", heading.Render(ui.UpdateAfterHeading), ansi.Wrap(content.AfterConfirmation, width, ""), "", theme.Muted.Render(ansi.Wrap(ui.UpdateRetryNote, width, "")))
	return strings.Split(ansi.Hardwrap(strings.Join(blocks, "\n"), width, true), "\n")
}
