package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

type Model struct {
	pages     map[ui.PageID]ui.Page
	rail      []ui.PageID
	railIndex int
	active    ui.PageID
	focus     ui.Focus
	inputMode ui.InputMode
	modal     *Modal
	width     int
	height    int
	theme     ui.Theme
}

func NewModel() Model {
	rail := ui.RailPages()
	pages := make(map[ui.PageID]ui.Page, len(rail))
	for _, id := range rail {
		pages[id] = ui.NewUnavailablePage(id)
	}
	active := rail[0]
	model := Model{
		pages: pages, rail: rail, active: active,
		focus: ui.Focus{Area: ui.FocusRail, Page: active},
		width: 100, height: 28, theme: ui.DefaultTheme(),
	}
	model.resizePages()
	return model
}

func (Model) Init() tea.Cmd { return nil }

func (model Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := message.(type) {
	case tea.WindowSizeMsg:
		model.width, model.height = typed.Width, typed.Height
		model.resizePages()
		return model, nil
	case ui.FocusRailMsg:
		model.focus = ui.Focus{Area: ui.FocusRail, Page: model.active}
		return model, nil
	case ui.InputModeMsg:
		model.inputMode = typed.Mode
		return model, nil
	}

	key, isKey := message.(tea.KeyPressMsg)
	if !isKey {
		return model.dispatchPage(message)
	}
	if key.String() == "ctrl+c" {
		return model, tea.Quit
	}
	if model.modal != nil {
		switch model.modal.Update(key) {
		case ModalClose:
			model.modal = nil
		case ModalConfirm:
			result := ModalConfirmedMsg{Title: model.modal.title, Object: model.modal.object}
			model.modal = nil
			return model, func() tea.Msg { return result }
		}
		return model, nil
	}
	name := routedKey(key, model.inputMode)
	if name == "?" {
		model.modal = NewDetail(ui.HelpTitle, ui.HelpBody)
		return model, nil
	}
	if name == "q" && model.inputMode != ui.InputText {
		return model, tea.Quit
	}
	if Classify(model.width, model.height) == ui.TooSmall {
		return model, nil
	}
	if model.focus.Area == ui.FocusRail {
		return model.updateRail(name)
	}
	return model.dispatchPage(message)
}

func (model Model) updateRail(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up":
		model.railIndex = max(0, model.railIndex-1)
	case "down":
		model.railIndex = min(len(model.rail)-1, model.railIndex+1)
	case "enter", "right":
		model.focus = ui.Focus{Area: ui.FocusContent, Page: model.active}
		model.pages[model.active].FocusFirst()
		return model, nil
	default:
		return model, nil
	}
	model.active = model.rail[model.railIndex]
	model.focus.Page = model.active
	return model, nil
}

func (model Model) dispatchPage(message tea.Msg) (tea.Model, tea.Cmd) {
	page := model.pages[model.active]
	if page == nil || model.focus.Area != ui.FocusContent {
		return model, nil
	}
	updated, command := page.Update(routedMessage(message, model.inputMode))
	model.pages[model.active] = updated
	return model, command
}

func (model Model) View() tea.View {
	layout := calculateLayout(model.width, model.height)
	var content string
	if layout.Class == ui.TooSmall {
		content = lipgloss.Place(model.width, model.height, lipgloss.Center, lipgloss.Center,
			model.theme.Title.Render(ui.ResizeRequired)+"\n"+model.theme.Muted.Render(ui.ResizeInstructions))
	} else {
		rail := ui.RenderRail(model.theme, model.rail, model.railIndex, model.focus.Area == ui.FocusRail, layout.RailWidth, layout.ContentHeight)
		page := model.pages[model.active]
		body := model.theme.Content.Width(layout.ContentWidth).Height(layout.ContentHeight).Render(page.View())
		main := lipgloss.JoinHorizontal(lipgloss.Top, rail, body)
		footer := ui.FooterRail
		if model.focus.Area == ui.FocusContent {
			footer = ui.FooterContent
		}
		content = strings.TrimRight(main, "\n") + "\n" + model.theme.Footer.Width(model.width).Render(footer)
	}
	if model.modal != nil {
		content = model.modal.View(model.width, model.height)
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = ui.AppName
	return view
}

func (model Model) resizePages() {
	layout := calculateLayout(model.width, model.height)
	for _, page := range model.pages {
		page.SetSize(layout.ContentWidth, layout.ContentHeight)
	}
}
