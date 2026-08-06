package tui

import (
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
)

type Modal struct {
	kind     modalKind
	title    string
	body     string
	object   string
	impact   string
	rollback string
	selected int
}

func NewDetail(title, body string) *Modal {
	return &Modal{kind: modalDetail, title: title, body: body}
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
