package system

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

const (
	rowDaemon      = "daemon"
	rowCore        = "core"
	rowCoreUpdate  = "core-update"
	rowCoreRestart = "core-restart"
	rowEndpoints   = "endpoints"
	rowRunSetup    = "run-setup"
	rowTUN         = "tun"
	rowService     = "service"
)

type Client interface {
	Onboarding(context.Context) (protocol.OnboardingStatus, error)
	Core(context.Context) (protocol.CoreStatus, error)
	InstallCore(context.Context, protocol.MutationRequest) (protocol.CoreInstallResult, error)
	RestartCore(context.Context, protocol.MutationRequest) (protocol.MutationResult, error)
}

type row struct {
	id      string
	section string
	label   string
	value   string
	detail  string
}

type onboardingResultMsg struct {
	status protocol.OnboardingStatus
	err    error
}

type actionKind uint8

const (
	actionUpdate actionKind = iota
	actionRestart
)

type actionStartMsg struct {
	kind        actionKind
	operationID string
	revision    uint64
}

type actionResultMsg struct {
	kind    actionKind
	install protocol.CoreInstallResult
	restart protocol.MutationResult
	err     error
}

type Model struct {
	ctx              context.Context
	client           Client
	newOperationID   func() string
	status           protocol.Status
	core             protocol.CoreStatus
	onboarding       protocol.OnboardingStatus
	focusID          string
	detail           *row
	pending          bool
	mutationsEnabled bool
	lastError        string
	width            int
	height           int
	theme            ui.Theme
}

func New(client Client, newOperationID func() string) *Model {
	return NewWithContext(context.Background(), client, newOperationID)
}

func NewWithContext(ctx context.Context, client Client, newOperationID func() string) *Model {
	if ctx == nil {
		ctx = context.Background()
	}
	if newOperationID == nil {
		newOperationID = defaultOperationID
	}
	return &Model{ctx: ctx, client: client, newOperationID: newOperationID, focusID: rowDaemon, theme: ui.DefaultTheme()}
}

func (m *Model) ID() ui.PageID { return ui.PageSystem }

func (m *Model) SetSize(width, height int) { m.width, m.height = width, height }

func (m *Model) FocusFirst() {
	if m.rowIndex(m.focusID) < 0 {
		m.focusID = rowDaemon
	}
}

func (m *Model) SetSnapshot(status protocol.Status, core protocol.CoreStatus) {
	m.status, m.core = status, core
}

func (m *Model) SetOnboarding(status protocol.OnboardingStatus) { m.onboarding = status }

func (m *Model) SetMutationsEnabled(enabled bool) { m.mutationsEnabled = enabled }

func (m *Model) Load() tea.Cmd {
	if m.client == nil || !m.hasCapability(protocol.CapabilityOnboarding) {
		return nil
	}
	return func() tea.Msg {
		status, err := m.client.Onboarding(m.ctx)
		return onboardingResultMsg{status: status, err: err}
	}
}

func (m *Model) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	switch typed := message.(type) {
	case onboardingResultMsg:
		if typed.err != nil {
			m.lastError = ui.SystemStateUnavailable
		} else {
			m.lastError = ""
			m.onboarding = typed.status
		}
		return m, nil
	case ui.CoreObservedMsg:
		m.core = typed.Core
		return m, nil
	case actionStartMsg:
		if m.pending {
			return m, nil
		}
		m.pending = true
		return m, m.runAction(typed)
	case actionResultMsg:
		m.pending = false
		if typed.err != nil {
			var apiError protocol.APIError
			if errors.As(typed.err, &apiError) && apiError.Code == protocol.CodeRevisionConflict {
				m.lastError = ui.SystemChangedMessage
				return m, tea.Batch(m.Load(), m.loadCore())
			}
			m.lastError = ui.SystemActionFailed
			return m, nil
		}
		m.lastError = ""
		revision := typed.restart.Revision
		if typed.kind == actionUpdate {
			revision = typed.install.Revision
		}
		return m, tea.Batch(m.Load(), m.loadCore(), func() tea.Msg { return ui.RuntimeRevisionMsg{Revision: revision} })
	}

	key, ok := message.(tea.KeyPressMsg)
	if m.detail != nil {
		if ok && (key.String() == "esc" || key.String() == "enter") {
			m.detail = nil
		}
		return m, nil
	}
	if !ok {
		return m, nil
	}
	rows := m.rows()
	index := m.rowIndex(m.focusID)
	switch key.String() {
	case "left":
		return m, func() tea.Msg { return ui.FocusRailMsg{} }
	case "up":
		if index > 0 {
			m.focusID = rows[index-1].id
		}
	case "down":
		if index >= 0 && index+1 < len(rows) {
			m.focusID = rows[index+1].id
		}
	case "enter":
		if index < 0 {
			return m, nil
		}
		switch m.focusID {
		case rowEndpoints, rowRunSetup:
			if m.client == nil || !m.mutationsEnabled || !m.hasCapability(protocol.CapabilityOnboarding) {
				return m, nil
			}
			return m, func() tea.Msg { return ui.RouteRequestMsg{Page: ui.PageSetup} }
		case rowCoreUpdate:
			if m.client == nil || !m.mutationsEnabled || !m.hasCapability(protocol.CapabilityCore) {
				return m, nil
			}
			return m, m.confirmAction(actionUpdate)
		case rowCoreRestart:
			if m.client == nil || !m.mutationsEnabled || !m.hasCapability(protocol.CapabilityCore) {
				return m, nil
			}
			return m, m.confirmAction(actionRestart)
		default:
			selected := rows[index]
			m.detail = &selected
		}
	}
	return m, nil
}

func (m *Model) View() string {
	if m.detail != nil {
		return m.theme.Content.Width(m.width).Height(m.height).Render(
			m.theme.Title.Render(m.detail.label+" details") + "\n\n" + m.detail.detail + "\n\n" + ui.EscCloseHint,
		)
	}
	lines := []string{m.theme.Title.Render(ui.SystemTitle), ""}
	section := ""
	for _, item := range m.rows() {
		if item.section != section {
			section = item.section
			if len(lines) > 2 {
				lines = append(lines, "")
			}
			lines = append(lines, m.theme.Title.Render(section))
		}
		marker := "  "
		if item.id == m.focusID {
			marker = "> "
		}
		value := item.value
		if value != "" {
			value = "  " + value
		}
		lines = append(lines, marker+item.label+value)
	}
	if m.pending {
		lines = append(lines, "", ui.PendingLabel)
	}
	if m.lastError != "" {
		lines = append(lines, "", m.lastError)
	}
	return m.theme.Content.Width(m.width).Height(m.height).Render(strings.Join(lines, "\n"))
}

func (m *Model) rows() []row {
	configState := ui.UnavailableTitle
	if m.status.Config != nil {
		configState = fmt.Sprintf("%s · desired %d / observed %d", m.status.Config.Status, m.status.Config.DesiredRevision, m.status.Config.ObservedRevision)
	}
	daemon := fmt.Sprintf("Version %s\nUptime %s\nHealth %s\nRevision %d\nConfig %s", valueOr(m.status.DaemonVersion, ui.UnknownLabel), uptime(m.status.StartedAt), valueOr(m.status.Health, ui.UnknownLabel), m.status.Revision, configState)
	core := fmt.Sprintf("Status %s\nVersion %s\nPID %d\nRestarts %d", valueOr(m.core.Status, ui.UnknownLabel), valueOr(m.core.Version, ui.UnknownLabel), m.core.PID, m.core.Restarts)
	endpoints := fmt.Sprintf("Mixed %s\nController %s\nWeb GUI %s", valueOr(m.onboarding.MixedAddr, ui.MissingValue), valueOr(m.onboarding.ControllerAddr, ui.MissingValue), valueOr(m.onboarding.WebAddr, ui.MissingValue))
	return []row{
		{id: rowDaemon, section: ui.DaemonSectionTitle, label: ui.DaemonLabel, value: valueOr(m.status.DaemonVersion, ui.UnknownLabel) + " · " + valueOr(m.status.Health, ui.UnknownLabel), detail: daemon},
		{id: rowCore, section: ui.CoreSectionTitle, label: ui.MihomoCoreLabel, value: valueOr(m.core.Status, ui.UnknownLabel) + " · " + valueOr(m.core.Version, ui.UnknownLabel), detail: core},
		{id: rowCoreUpdate, section: ui.CoreSectionTitle, label: m.coreActionLabel(), value: actionState(m.pending, m.hasCapability(protocol.CapabilityCore), m.mutationsEnabled), detail: ui.UpdateCoreImpact},
		{id: rowCoreRestart, section: ui.CoreSectionTitle, label: ui.RestartCoreLabel, value: actionState(m.pending, m.hasCapability(protocol.CapabilityCore), m.mutationsEnabled), detail: ui.RestartCoreImpact},
		{id: rowEndpoints, section: ui.LocalEndpointsLabel, label: ui.LocalEndpointsLabel, value: endpointSummary(m.onboarding), detail: endpoints},
		{id: rowRunSetup, section: ui.MaintenanceSectionTitle, label: ui.RunSetupLabel, detail: ui.RunSetupDetail},
		{id: rowTUN, section: ui.FutureCapabilitiesTitle, label: ui.TUNLabel, value: ui.UnavailableTitle, detail: ui.TUNUnavailableDetail},
		{id: rowService, section: ui.FutureCapabilitiesTitle, label: ui.SystemServiceLabel, value: ui.UnavailableTitle, detail: ui.ServiceUnavailableDetail},
	}
}

func (m *Model) rowIndex(id string) int {
	for index, item := range m.rows() {
		if item.id == id {
			return index
		}
	}
	return -1
}

func (m *Model) currentRevision() uint64 {
	return max(m.status.Revision, m.core.Revision, m.onboarding.Revision)
}

func (m *Model) confirmAction(kind actionKind) tea.Cmd {
	revision, operationID := m.currentRevision(), m.newOperationID()
	title, object, impact, rollback := ui.UpdateCoreTitle, ui.MihomoCoreLabel, ui.UpdateCoreImpact, ui.UpdateCoreRollback
	if kind == actionUpdate && m.core.Version == "" {
		title, impact = ui.InstallCoreTitle, ui.InstallCoreImpact
	}
	if kind == actionRestart {
		title, impact, rollback = ui.RestartCoreTitle, ui.RestartCoreImpact, ui.RestartCoreRollback
	}
	return func() tea.Msg {
		return ui.ConfirmationRequestMsg{Title: title, Object: object, Impact: impact, Rollback: rollback, OnConfirm: func() tea.Msg {
			return actionStartMsg{kind: kind, operationID: operationID, revision: revision}
		}}
	}
}

func (m *Model) coreActionLabel() string {
	if m.core.Version == "" {
		return ui.InstallCoreLabel
	}
	return ui.UpdateCoreLabel
}

func (m *Model) runAction(start actionStartMsg) tea.Cmd {
	return func() tea.Msg {
		revision := start.revision
		request := protocol.MutationRequest{OperationID: start.operationID, IfRevision: &revision}
		if start.kind == actionUpdate {
			result, err := m.client.InstallCore(m.ctx, request)
			return actionResultMsg{kind: start.kind, install: result, err: err}
		}
		result, err := m.client.RestartCore(m.ctx, request)
		return actionResultMsg{kind: start.kind, restart: result, err: err}
	}
}

func (m *Model) loadCore() tea.Cmd {
	if m.client == nil || !m.hasCapability(protocol.CapabilityCore) {
		return nil
	}
	return func() tea.Msg {
		core, err := m.client.Core(m.ctx)
		if err != nil {
			return actionResultMsg{err: err}
		}
		return ui.CoreObservedMsg{Core: core}
	}
}

func endpointSummary(status protocol.OnboardingStatus) string {
	return fmt.Sprintf("%s · %s · %s", valueOr(status.MixedAddr, ui.MissingValue), valueOr(status.ControllerAddr, ui.MissingValue), valueOr(status.WebAddr, ui.MissingValue))
}

func actionState(pending, available, enabled bool) string {
	if !available {
		return ui.UnavailableTitle
	}
	if !enabled {
		return ui.StaleLabel
	}
	if pending {
		return ui.PendingLabel
	}
	return ""
}

func (m *Model) hasCapability(capability string) bool {
	return slices.Contains(m.status.Capabilities, capability)
}

func uptime(started time.Time) string {
	if started.IsZero() {
		return ui.UnknownLabel
	}
	duration := time.Since(started)
	if duration < 0 {
		duration = 0
	}
	return duration.Round(time.Second).String()
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

var fallbackOperationID atomic.Uint64

func defaultOperationID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return "tui-system-" + hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("tui-system-%d-%d", time.Now().UnixNano(), fallbackOperationID.Add(1))
}
