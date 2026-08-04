package webgui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/platform"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

// Client is the typed control surface used by the Web GUI page.
type Client interface {
	WebGUI(context.Context) (protocol.WebGUIStatus, error)
	InstallPanel(context.Context, string, protocol.PanelInstallRequest) (protocol.MutationResult, error)
	UpdatePanel(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
	ActivatePanel(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
	RollbackPanel(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
	OpenWebGUI(context.Context) (protocol.WebGUIOpenResult, error)
}

type statusResultMsg struct {
	status protocol.WebGUIStatus
	err    error
}

type mutationDoneMsg struct {
	err error
}

// Model is the Web GUI lifecycle page.
type Model struct {
	ctx            context.Context
	client         Client
	openBrowser    func(string) error
	newOperationID func() string
	available      bool
	status         protocol.WebGUIStatus
	selected       int
	lastError      string
	toast          string
	contentFocused bool
	width          int
	height         int
	theme          ui.Theme
}

// New constructs a Web GUI page with background context.
func New(client Client, capabilities []string) *Model {
	return NewWithContext(context.Background(), client, capabilities)
}

// NewWithContext constructs a Web GUI page bound to ctx.
func NewWithContext(ctx context.Context, client Client, capabilities []string) *Model {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Model{
		ctx: ctx, client: client, available: slices.Contains(capabilities, protocol.CapabilityWebGUI),
		openBrowser: platform.OpenBrowser, newOperationID: randomOperationID, theme: ui.DefaultTheme(),
	}
}

// SetOpenBrowser injects the browser launcher (tests and headless environments).
func (m *Model) SetOpenBrowser(open func(string) error) {
	if open != nil {
		m.openBrowser = open
	}
}

// SetOperationID injects deterministic operation IDs for tests.
func (m *Model) SetOperationID(gen func() string) {
	if gen != nil {
		m.newOperationID = gen
	}
}

func (m *Model) ID() ui.PageID { return ui.PageWebGUI }

func (m *Model) SetSize(width, height int) { m.width, m.height = width, height }

// SetContentFocused reports whether the root shell has given keyboard focus to this page.
func (m *Model) SetContentFocused(focused bool) { m.contentFocused = focused }

func (m *Model) FocusFirst() {
	if len(m.status.Panels) > 0 {
		m.selected = 0
	}
}

func (m *Model) SetCapabilities(capabilities []string) {
	m.available = slices.Contains(capabilities, protocol.CapabilityWebGUI)
}

func (m *Model) SetStatus(status protocol.WebGUIStatus) {
	status.Panels = append([]protocol.PanelStatus(nil), status.Panels...)
	m.status = status
	if m.selected >= len(m.status.Panels) {
		m.selected = max(0, len(m.status.Panels)-1)
	}
}

// FooterHints returns contextual shortcuts for the root shell footer.
func (m *Model) FooterHints() string {
	if !m.available {
		return ui.FooterWebGUI
	}
	return ui.FooterWebGUIActions
}

func (m *Model) Load() tea.Cmd {
	if !m.available || m.client == nil {
		return nil
	}
	return func() tea.Msg {
		status, err := m.client.WebGUI(m.ctx)
		return statusResultMsg{status: status, err: err}
	}
}

func (m *Model) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	switch typed := message.(type) {
	case statusResultMsg:
		if typed.err != nil {
			m.lastError = ui.WebGUIUnavailable
		} else {
			m.lastError = ""
			m.SetStatus(typed.status)
		}
		return m, nil
	case mutationDoneMsg:
		if typed.err != nil {
			m.toast = typed.err.Error()
		} else {
			m.toast = ""
		}
		return m, m.Load()
	case tea.KeyPressMsg:
		if !m.available {
			if typed.String() == "left" {
				return m, func() tea.Msg { return ui.FocusRailMsg{} }
			}
			return m, nil
		}
		return m, m.handleKey(typed.String())
	}
	return m, nil
}

func (m *Model) handleKey(name string) tea.Cmd {
	switch name {
	case "left":
		return func() tea.Msg { return ui.FocusRailMsg{} }
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
	case "down", "j":
		if m.selected+1 < len(m.status.Panels) {
			m.selected++
		}
	case "space":
		return m.activateSelected()
	case "o":
		return m.openBrowserAction()
	case "i":
		return m.installSelected()
	case "u":
		return m.updateSelected()
	case "b":
		return m.rollbackSelected()
	}
	return nil
}

func (m *Model) selectedPanel() (protocol.PanelStatus, bool) {
	if m.selected < 0 || m.selected >= len(m.status.Panels) {
		return protocol.PanelStatus{}, false
	}
	return m.status.Panels[m.selected], true
}

func (m *Model) activateSelected() tea.Cmd {
	panel, ok := m.selectedPanel()
	if !ok || m.client == nil {
		return nil
	}
	operationID := m.newOperationID()
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionActivatePanel, Page: ui.PageWebGUI, Capability: protocol.CapabilityWebGUI,
			Key: "panel:activate:" + panel.ID, Title: ui.ActivatePanelTitle, Object: panel.Name,
			Impact: ui.ActivatePanelImpact, Rollback: ui.ActivatePanelRollback,
			Execute: func() tea.Msg {
				_, err := m.client.ActivatePanel(m.ctx, panel.ID, protocol.MutationRequest{OperationID: operationID})
				return mutationDoneMsg{err: err}
			},
		}
	}
}

func (m *Model) installSelected() tea.Cmd {
	panel, ok := m.selectedPanel()
	if !ok || m.client == nil {
		return nil
	}
	operationID := m.newOperationID()
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionInstallPanel, Page: ui.PageWebGUI, Capability: protocol.CapabilityWebGUI,
			Key: "panel:install:" + panel.ID, Title: ui.InstallPanelTitle, Object: panel.Name,
			Impact: ui.InstallPanelImpact, Rollback: ui.InstallPanelRollback,
			Execute: func() tea.Msg {
				_, err := m.client.InstallPanel(m.ctx, panel.ID, protocol.PanelInstallRequest{OperationID: operationID})
				return mutationDoneMsg{err: err}
			},
		}
	}
}

func (m *Model) updateSelected() tea.Cmd {
	panel, ok := m.selectedPanel()
	if !ok || m.client == nil {
		return nil
	}
	operationID := m.newOperationID()
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionUpdatePanel, Page: ui.PageWebGUI, Capability: protocol.CapabilityWebGUI,
			Key: "panel:update:" + panel.ID, Title: ui.UpdatePanelTitle, Object: panel.Name,
			Impact: ui.UpdatePanelImpact, Rollback: ui.UpdatePanelRollback,
			Execute: func() tea.Msg {
				_, err := m.client.UpdatePanel(m.ctx, panel.ID, protocol.MutationRequest{OperationID: operationID})
				return mutationDoneMsg{err: err}
			},
		}
	}
}

func (m *Model) rollbackSelected() tea.Cmd {
	panel, ok := m.selectedPanel()
	if !ok || m.client == nil || panel.RollbackBuild == "" {
		return nil
	}
	operationID := m.newOperationID()
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionRollbackPanel, Page: ui.PageWebGUI, Capability: protocol.CapabilityWebGUI,
			Key: "panel:rollback:" + panel.ID, Title: ui.RollbackPanelTitle, Object: panel.Name,
			Impact: ui.RollbackPanelImpact, Rollback: ui.RollbackPanelRollback,
			Execute: func() tea.Msg {
				_, err := m.client.RollbackPanel(m.ctx, panel.ID, protocol.MutationRequest{OperationID: operationID})
				return mutationDoneMsg{err: err}
			},
		}
	}
}

func (m *Model) openBrowserAction() tea.Cmd {
	if m.client == nil {
		return nil
	}
	openBrowser := m.openBrowser
	if openBrowser == nil {
		openBrowser = platform.OpenBrowser
	}
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionOpenWebGUI, Page: ui.PageWebGUI, Capability: protocol.CapabilityWebGUI,
			Key: "web-gui:open", Title: ui.OpenWebGUITitle, Object: ui.WebGUITitle,
			Impact: ui.OpenWebGUIImpact, Rollback: ui.OpenWebGUIRollback,
			Execute: func() tea.Msg {
				// Token stays inside this command; never surface it in the view.
				result, err := m.client.OpenWebGUI(m.ctx)
				if err != nil {
					return mutationDoneMsg{err: err}
				}
				if err := openBrowser(result.OpenURL); err != nil {
					return mutationDoneMsg{err: err}
				}
				return mutationDoneMsg{}
			},
		}
	}
}

func (m *Model) View() string {
	if !m.available {
		return m.theme.Content.Width(m.width).Height(m.height).Render(
			m.theme.Title.Render(ui.WebGUITitle) + "\n\n" + m.theme.Muted.Render(ui.UnavailableTitle+": "+ui.WebGUILifecycleUnavailable),
		)
	}
	active := valueOr(m.status.ActivePanel, ui.MissingValue)
	header := fmt.Sprintf("%s  %s  %s %s  %s %d  %s",
		valueOr(m.status.GatewayHealth, ui.UnknownLabel), valueOr(m.status.GatewayAddr, ui.MissingValue),
		ui.ActivePanelLabel, active, ui.BrowserSessionsLabel, m.status.BrowserSessions, ui.OpenBrowserHint)
	lines := []string{m.theme.Title.Render(ui.WebGUITitle), header, ""}
	if len(m.status.Panels) == 0 {
		lines = append(lines, ui.NoWebGUIPanels)
	}
	for index, panel := range m.status.Panels {
		state := ""
		if panel.Active {
			state = "  " + ui.ActiveLabel
		}
		// Focus marker sits outside the card so the border is not interrupted.
		cursor := "  "
		selected := index == m.selected
		if selected {
			cursor = ui.FocusMarker
		}
		body := fmt.Sprintf("Installed  %s\nLatest     %s\nHealth     %s\nRollback   %s",
			valueOr(panel.InstalledBuild, ui.MissingValue), valueOr(panel.LatestBuild, ui.MissingValue),
			valueOr(panel.Health, ui.UnknownLabel), valueOr(panel.RollbackBuild, ui.MissingValue))
		border := m.theme.ColorSurfaceBorder
		// Accent the focused panel only while content owns keyboard focus.
		if selected && m.contentFocused {
			border = m.theme.ColorAccent
		}
		card := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Padding(0, 1).
			Width(max(28, min(56, m.width-4))).
			Render(m.theme.Title.Render(valueOr(panel.Name, panel.ID)+state) + "\n" + body)
		lines = append(lines, cursor+card)
	}
	safeguards := []string{
		boolState("Loopback binding", m.status.Safeguards.LoopbackBound),
		boolState("Browser authentication", m.status.Safeguards.BrowserAuthenticated),
		boolState("Controller isolation", m.status.Safeguards.ControllerIsolated),
		boolState("Mutation coordinator", m.status.Safeguards.MutationsCoordinated),
	}
	lines = append(lines, "", m.theme.Title.Render(ui.GatewaySafeguardsTitle), strings.Join(safeguards, "  "))
	if m.lastError != "" {
		lines = append(lines, "", m.lastError)
	}
	if m.toast != "" {
		lines = append(lines, "", m.toast)
	}
	// Never render auth tokens or open URLs.
	view := m.theme.Content.Width(m.width).Height(m.height).Render(strings.Join(lines, "\n"))
	lower := strings.ToLower(view)
	if strings.Contains(lower, "token=") || strings.Contains(lower, "open_url") {
		return m.theme.Content.Width(m.width).Height(m.height).Render(ui.WebGUITitle + "\n\n" + ui.WebGUIUnavailable)
	}
	return view
}

func boolState(label string, enabled bool) string {
	state := ui.OffLabel
	if enabled {
		state = ui.OnLabel
	}
	return label + " " + state
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func randomOperationID() string {
	var value [8]byte
	_, _ = rand.Read(value[:])
	return hex.EncodeToString(value[:])
}
