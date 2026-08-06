package webgui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// Client is the typed control surface used by the Web GUI page.
type Client interface {
	WebGUI(context.Context) (protocol.WebGUIStatus, error)
	InstallPanel(context.Context, string, protocol.PanelInstallRequest) (protocol.MutationResult, error)
	UpdatePanel(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
	ActivatePanel(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
	RollbackPanel(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
	UninstallPanel(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
	ReinstallPanel(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
	OpenWebGUI(context.Context, string) (protocol.WebGUIOpenResult, error)
}

type statusResultMsg struct {
	status protocol.WebGUIStatus
	err    error
}

type mutationDoneMsg struct {
	err error
}

// Err implements the shell's action-outcome contract so panel lifecycle
// mutations are classified Succeeded/Failed in the Recent operations ledger.
func (m mutationDoneMsg) Err() error { return m.err }

var _ interface{ Err() error } = mutationDoneMsg{}

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
			if typed.String() == "esc" {
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
	case "esc":
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
	case "r":
		return m.reinstallSelected()
	case "x", "d":
		return m.uninstallSelected()
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

func (m *Model) uninstallSelected() tea.Cmd {
	panel, ok := m.selectedPanel()
	if !ok || m.client == nil {
		return nil
	}
	// Only uninstall when something is installed (or marked active).
	// InstalledBuild empty = not installed, same source of truth as the
	// rendered "Installed —" line (design W2).
	if panel.InstalledBuild == "" && !panel.Active {
		return nil
	}
	operationID := m.newOperationID()
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionUninstallPanel, Page: ui.PageWebGUI, Capability: protocol.CapabilityWebGUI,
			Key: "panel:uninstall:" + panel.ID, Title: ui.UninstallPanelTitle, Object: panel.Name,
			Impact: ui.UninstallPanelImpact, Rollback: ui.UninstallPanelRollback,
			Execute: func() tea.Msg {
				_, err := m.client.UninstallPanel(m.ctx, panel.ID, protocol.MutationRequest{OperationID: operationID})
				return mutationDoneMsg{err: err}
			},
		}
	}
}

func (m *Model) reinstallSelected() tea.Cmd {
	panel, ok := m.selectedPanel()
	if !ok || m.client == nil {
		return nil
	}
	operationID := m.newOperationID()
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionReinstallPanel, Page: ui.PageWebGUI, Capability: protocol.CapabilityWebGUI,
			Key: "panel:reinstall:" + panel.ID, Title: ui.ReinstallPanelTitle, Object: panel.Name,
			Impact: ui.ReinstallPanelImpact, Rollback: ui.ReinstallPanelRollback,
			Execute: func() tea.Msg {
				_, err := m.client.ReinstallPanel(m.ctx, panel.ID, protocol.MutationRequest{OperationID: operationID})
				return mutationDoneMsg{err: err}
			},
		}
	}
}

func (m *Model) openBrowserAction() tea.Cmd {
	if m.client == nil {
		return nil
	}
	panel, ok := m.selectedPanel()
	if !ok {
		return nil
	}
	openBrowser := m.openBrowser
	if openBrowser == nil {
		openBrowser = platform.OpenBrowser
	}
	object := panel.Name
	if object == "" {
		object = panel.ID
	}
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionOpenWebGUI, Page: ui.PageWebGUI, Capability: protocol.CapabilityWebGUI,
			Key: "web-gui:open:" + panel.ID, Title: ui.OpenWebGUITitle, Object: object,
			Impact: ui.OpenWebGUIImpact, Rollback: ui.OpenWebGUIRollback,
			Execute: func() tea.Msg {
				// Token stays inside this command; never surface it in the view.
				// Open the focused panel at /__mihari/panels/{id}/ so panels can run concurrently.
				result, err := m.client.OpenWebGUI(m.ctx, panel.ID)
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

func (m *Model) layoutWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 100
}

func (m *Model) View() string {
	inner := ui.FullSectionInner(m.layoutWidth())
	if !m.available {
		body := m.theme.Muted.Render(ui.UnavailableTitle + ": " + ui.WebGUILifecycleUnavailable)
		return ui.RenderBorderedSection(m.theme, ui.WebGUITitle, body, inner)
	}
	active := valueOr(m.status.ActivePanel, ui.MissingValue)
	// Two summary lines (design W1): health+addr, then Active panel + sessions.
	// OpenBrowserHint moved out — the footer already declares the o key.
	textW := ui.SectionTextWidth(inner)
	line1 := ui.TruncateVisible(valueOr(m.status.GatewayHealth, ui.UnknownLabel)+"  "+valueOr(m.status.GatewayAddr, ui.MissingValue), textW)
	line2 := ui.TruncateVisible(fmt.Sprintf("%s %s  ·  %s %d", ui.ActivePanelLabel, active, ui.BrowserSessionsLabel, m.status.BrowserSessions), textW)
	header := line1 + "\n" + line2
	var parts []string
	parts = append(parts, ui.RenderBorderedSection(m.theme, ui.WebGUITitle, header, inner))

	if len(m.status.Panels) == 0 {
		parts = append(parts, ui.RenderBorderedSection(m.theme, "Panels", m.theme.Muted.Render(ui.NoWebGUIPanels), inner))
	}
	for index, panel := range m.status.Panels {
		state := ""
		if panel.Active {
			state = "  " + ui.ActiveLabel
		}
		selected := index == m.selected
		marker := "  "
		if selected {
			marker = ui.FocusMarker
		}
		body := fmt.Sprintf("%sInstalled  %s\n  Latest     %s\n  Health     %s\n  Rollback   %s",
			marker, valueOr(panel.InstalledBuild, ui.MissingValue), valueOr(panel.LatestBuild, ui.MissingValue),
			valueOr(panel.Health, ui.UnknownLabel), valueOr(panel.RollbackBuild, ui.MissingValue))
		title := valueOr(panel.Name, panel.ID) + state
		border := m.theme.ColorSurfaceBorder
		// Accent the focused panel only while content owns keyboard focus.
		if selected && m.contentFocused {
			border = m.theme.ColorAccent
		}
		parts = append(parts, ui.RenderBorderedSectionWithBorder(m.theme, title, body, inner, border))
	}
	safeguards := []string{
		boolState("Loopback binding", m.status.Safeguards.LoopbackBound),
		boolState("Browser authentication", m.status.Safeguards.BrowserAuthenticated),
		boolState("Controller isolation", m.status.Safeguards.ControllerIsolated),
		boolState("Mutation coordinator", m.status.Safeguards.MutationsCoordinated),
	}
	// 2×2 layout (design W1); each line clips to the section text width.
	row1 := ui.TruncateVisible(safeguards[0]+"  "+safeguards[1], textW)
	row2 := ui.TruncateVisible(safeguards[2]+"  "+safeguards[3], textW)
	parts = append(parts, ui.RenderBorderedSection(m.theme, ui.GatewaySafeguardsTitle, row1+"\n"+row2, inner))
	if m.lastError != "" {
		parts = append(parts, m.lastError)
	}
	if m.toast != "" {
		parts = append(parts, m.toast)
	}
	// Never render auth tokens or open URLs.
	view := strings.Join(parts, "\n")
	lower := strings.ToLower(view)
	if strings.Contains(lower, "token=") || strings.Contains(lower, "open_url") {
		return ui.RenderBorderedSection(m.theme, ui.WebGUITitle, ui.WebGUIUnavailable, inner)
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
