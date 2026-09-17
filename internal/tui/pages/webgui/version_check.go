package webgui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type panelVersionState struct {
	latest           string
	checking, failed bool
}
type panelVersionMsg struct {
	id     string
	result protocol.VersionCheck
	err    error
}

func (m panelVersionMsg) Err() error                { return m.err }
func (m panelVersionMsg) DiagnosticPage() ui.PageID { return ui.PageWebGUI }

func (m *Model) checkPanelVersions() tea.Cmd {
	checker, ok := m.client.(interface {
		CheckPanelVersion(context.Context, string) (protocol.VersionCheck, error)
	})
	if !ok {
		return nil
	}
	if m.versions == nil {
		m.versions = make(map[string]panelVersionState)
	}
	var commands []tea.Cmd
	ctx := m.ctx
	for _, panel := range m.status.Panels {
		id := panel.ID
		state := m.versions[id]
		if state.checking {
			continue
		}
		state.checking = true
		state.failed = false
		m.versions[id] = state
		commands = append(commands, func() tea.Msg {
			result, err := checker.CheckPanelVersion(ctx, id)
			return ui.PageResultMsg{Page: ui.PageWebGUI, Result: panelVersionMsg{id: id, result: result, err: err}}
		})
	}
	return tea.Batch(commands...)
}

func (m *Model) latestLabel(panel protocol.PanelStatus) string {
	state, ok := m.versions[panel.ID]
	if !ok {
		return valueOr(panel.LatestBuild, ui.UnknownLabel)
	}
	if state.checking {
		return "Checking…"
	}
	if state.failed {
		return "Check failed"
	}
	latest := diagnostics.EscapeTerminal(state.latest)
	if panel.InstalledBuild == state.latest {
		return latest + " · Up to date"
	}
	if panel.InstalledBuild != "" {
		return latest + " · available"
	}
	return latest
}
