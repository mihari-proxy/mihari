package system

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type coreVersionState struct {
	latest, channel  string
	checking, failed bool
	generation       uint64
}
type coreVersionMsg struct {
	generation uint64
	result     protocol.VersionCheck
	err        error
}

func (m coreVersionMsg) Err() error                { return m.err }
func (m coreVersionMsg) DiagnosticPage() ui.PageID { return ui.PageSystem }

func (m *Model) checkCoreVersion() tea.Cmd {
	checker, ok := m.client.(interface {
		CheckCoreVersion(context.Context) (protocol.VersionCheck, error)
	})
	if !ok || !m.hasCapability(protocol.CapabilityCore) {
		return nil
	}
	channel := coreChannelName(m.core.Channel)
	// A check already running for this channel keeps its generation. Selecting
	// the page again, or entering it, must not drop that result.
	if m.coreVersion.channel == channel && m.coreVersion.checking {
		return nil
	}
	m.coreVersion = coreVersionState{checking: true, channel: channel, generation: m.coreVersion.generation + 1}
	generation, ctx := m.coreVersion.generation, m.ctx
	check := func() tea.Msg {
		result, err := checker.CheckCoreVersion(ctx)
		return ui.PageResultMsg{Page: ui.PageSystem, Result: coreVersionMsg{generation: generation, result: result, err: err}}
	}
	return tea.Batch(check, m.rowSpinCmdIfNeeded())
}

func (m *Model) coreUpdateValue() string {
	if !m.hasCapability(protocol.CapabilityCore) || !m.mutationsEnabled {
		return actionState(m.hasCapability(protocol.CapabilityCore), m.mutationsEnabled)
	}
	state := m.coreVersion
	if state.generation == 0 {
		return actionState(true, true)
	}
	if state.channel != coreChannelName(m.core.Channel) {
		return ui.UnknownLabel
	}
	if state.checking {
		return "Checking…"
	}
	if state.failed {
		return "Check failed"
	}
	if state.latest == "" {
		return ui.UnknownLabel
	}
	current := valueOr(m.core.Version, m.core.LocalVersion)
	if current == state.latest {
		return diagnostics.EscapeTerminal(state.latest) + " · Up to date"
	}
	if current == "" {
		current = ui.UnknownLabel
	}
	return updateAvailableValue(diagnostics.EscapeTerminal(current), diagnostics.EscapeTerminal(state.latest))
}

func updateAvailableValue(current, latest string) string {
	return current + " -> " + latest + " " + ui.UpdateMihariAvailable
}
