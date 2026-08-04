package webgui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

type Client interface {
	WebGUI(context.Context) (protocol.WebGUIStatus, error)
}

type statusResultMsg struct {
	status protocol.WebGUIStatus
	err    error
}

type Model struct {
	ctx       context.Context
	client    Client
	available bool
	status    protocol.WebGUIStatus
	lastError string
	width     int
	height    int
	theme     ui.Theme
}

func New(client Client, capabilities []string) *Model {
	return NewWithContext(context.Background(), client, capabilities)
}

func NewWithContext(ctx context.Context, client Client, capabilities []string) *Model {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Model{ctx: ctx, client: client, available: slices.Contains(capabilities, protocol.CapabilityWebGUI), theme: ui.DefaultTheme()}
}

func (m *Model) ID() ui.PageID { return ui.PageWebGUI }

func (m *Model) SetSize(width, height int) { m.width, m.height = width, height }

func (m *Model) FocusFirst() {}

func (m *Model) SetCapabilities(capabilities []string) {
	m.available = slices.Contains(capabilities, protocol.CapabilityWebGUI)
}

func (m *Model) SetStatus(status protocol.WebGUIStatus) {
	status.Panels = append([]protocol.PanelStatus(nil), status.Panels...)
	m.status = status
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
	if result, ok := message.(statusResultMsg); ok {
		if result.err != nil {
			m.lastError = ui.WebGUIUnavailable
		} else {
			m.lastError = ""
			m.SetStatus(result.status)
		}
		return m, nil
	}
	if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "left" {
		return m, func() tea.Msg { return ui.FocusRailMsg{} }
	}
	return m, nil
}

func (m *Model) View() string {
	if !m.available {
		return m.theme.Content.Width(m.width).Height(m.height).Render(
			m.theme.Title.Render(ui.WebGUITitle) + "\n\n" + m.theme.Muted.Render(ui.UnavailableTitle+": "+ui.WebGUIPhaseBoundary),
		)
	}
	active := valueOr(m.status.ActivePanel, ui.MissingValue)
	header := fmt.Sprintf("%s  %s  %s %s  %s %d",
		valueOr(m.status.GatewayHealth, ui.UnknownLabel), valueOr(m.status.GatewayAddr, ui.MissingValue),
		ui.ActivePanelLabel, active, ui.BrowserSessionsLabel, m.status.BrowserSessions)
	lines := []string{m.theme.Title.Render(ui.WebGUITitle), header, ""}
	if len(m.status.Panels) == 0 {
		lines = append(lines, ui.NoWebGUIPanels)
	}
	for _, panel := range m.status.Panels {
		state := ""
		if panel.Active {
			state = "  " + ui.ActiveLabel
		}
		body := fmt.Sprintf("Installed  %s\nLatest     %s\nHealth     %s\nRollback   %s",
			valueOr(panel.InstalledBuild, ui.MissingValue), valueOr(panel.LatestBuild, ui.MissingValue),
			valueOr(panel.Health, ui.UnknownLabel), valueOr(panel.RollbackBuild, ui.MissingValue))
		lines = append(lines, lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(max(28, min(56, m.width-4))).Render(
			m.theme.Title.Render(valueOr(panel.Name, panel.ID)+state)+"\n"+body,
		))
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
	return m.theme.Content.Width(m.width).Height(m.height).Render(strings.Join(lines, "\n"))
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
