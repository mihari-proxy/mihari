package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type ModalAction uint8

const (
	ModalNone ModalAction = iota
	ModalClose
	ModalConfirm
)

type ModalConfirmedMsg struct {
	Title  string
	Object string
}

type modalKind uint8

const (
	modalDetail modalKind = iota
	modalConfirmation
	modalHelp
)

type Modal struct {
	kind     modalKind
	title    string
	body     string
	object   string
	impact   string
	rollback string
	selected int
	scroll   int
}

func NewDetail(title, body string) *Modal {
	return &Modal{kind: modalDetail, title: title, body: body}
}

func NewHelp(title, body string) *Modal {
	return &Modal{kind: modalHelp, title: title, body: body}
}

func NewConfirmation(title, object, impact, rollback string) *Modal {
	return &Modal{kind: modalConfirmation, title: title, object: object, impact: impact, rollback: rollback, selected: 1}
}

func (m *Modal) Update(message tea.Msg) ModalAction {
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return ModalNone
	}
	switch key.String() {
	case "esc":
		return ModalClose
	}
	if m.kind == modalHelp {
		switch key.String() {
		case "up":
			m.scroll = max(0, m.scroll-1)
		case "down":
			m.scroll++
		}
		return ModalNone
	}
	if m.kind != modalConfirmation {
		return ModalNone
	}
	switch key.String() {
	case "left", "right", "tab", "shift+tab":
		m.selected = 1 - m.selected
	case "enter":
		if m.selected == 0 {
			return ModalConfirm
		}
		return ModalClose
	}
	return ModalNone
}

func (m *Modal) View(width, height int) string {
	theme := ui.DefaultTheme()
	if m.kind == modalHelp {
		return m.helpView(theme, width, height)
	}
	body := m.body
	if m.kind == modalConfirmation {
		// Plain-language confirmation: object, impact sentence, optional muted rollback.
		parts := make([]string, 0, 3)
		if m.object != "" {
			parts = append(parts, m.object)
		}
		if m.impact != "" {
			parts = append(parts, m.impact)
		}
		body = strings.Join(parts, "\n")
		if m.rollback != "" {
			if body != "" {
				body += "\n"
			}
			body += theme.Muted.Render(m.rollback)
		}
		confirm := theme.Button.Render(ui.ConfirmLabel)
		cancel := theme.Button.Render(ui.CancelLabel)
		if m.selected == 0 {
			confirm = theme.ButtonActive.Render(ui.ConfirmLabel)
		} else {
			cancel = theme.ButtonActive.Render(ui.CancelLabel)
		}
		body += "\n\n" + lipgloss.JoinHorizontal(lipgloss.Top, confirm, "  ", cancel)
	}
	boxWidth := min(64, max(24, width-8))
	content := theme.Dialog.Width(boxWidth).Render(theme.Title.Render(m.title) + "\n\n" + body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

func (m *Modal) helpView(theme ui.Theme, width, height int) string {
	boxWidth := min(64, max(24, width-8))
	maxBoxHeight := max(5, height-2)
	lines := strings.Split(m.body, "\n")
	visibleHeight := max(1, maxBoxHeight-4)
	start := min(m.scroll, max(0, len(lines)-visibleHeight))
	end := min(len(lines), start+visibleHeight)
	indicators := 0
	if start > 0 {
		indicators++
	}
	if end < len(lines) {
		indicators++
	}
	if indicators > 0 {
		contentRows := max(1, visibleHeight-indicators)
		start = min(m.scroll, max(0, len(lines)-contentRows))
		end = min(len(lines), start+contentRows)
	}
	m.scroll = start
	bodyLines := make([]string, 0, end-start+2)
	if start > 0 {
		bodyLines = append(bodyLines, theme.Muted.Render("▴"))
	}
	bodyLines = append(bodyLines, lines[start:end]...)
	if end < len(lines) {
		bodyLines = append(bodyLines, theme.Muted.Render(fmt.Sprintf("▾ %d more lines", len(lines)-end)))
	}
	content := theme.Dialog.Width(boxWidth).MaxHeight(maxBoxHeight).Render(
		theme.Title.Render(m.title) + "\n\n" + strings.Join(bodyLines, "\n"),
	)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}
