package webgui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"time"
)

type panelVersionState struct {
	latest           string
	checking, failed bool
	checkedAt        time.Time
	generation       uint64
}
type panelVersionMsg struct {
	id         string
	result     protocol.VersionCheck
	err        error
	generation uint64
}

func (m panelVersionMsg) Err() error                { return m.err }
func (m panelVersionMsg) DiagnosticPage() ui.PageID { return ui.PageWebGUI }

func (m *Model) checkPanelVersions() tea.Cmd {
	var commands []tea.Cmd
	for _, panel := range m.status.Panels {
		commands = append(commands, m.checkPanelVersion(panel.ID))
	}
	commands = append(commands, m.ensureVersionSpin())
	return tea.Batch(commands...)
}

func (m *Model) checkPanelVersion(id string) tea.Cmd {
	checker, ok := m.client.(interface {
		CheckPanelVersion(context.Context, string) (protocol.VersionCheck, error)
	})
	if !ok {
		return nil
	}
	if m.versions == nil {
		m.versions = make(map[string]panelVersionState)
	}
	ctx := m.ctx
	state := m.versions[id]
	if state.checking || (!state.failed && state.latest != "" && time.Since(state.checkedAt) < 5*time.Minute) {
		return nil
	}
	state.checking = true
	state.failed = false
	state.generation++
	m.versions[id] = state
	return func() tea.Msg {
		result, err := checker.CheckPanelVersion(ctx, id)
		return ui.PageResultMsg{Page: ui.PageWebGUI, Result: panelVersionMsg{id: id, result: result, err: err, generation: state.generation}}
	}
}

func (m *Model) anyVersionChecking() bool {
	for _, state := range m.versions {
		if state.checking {
			return true
		}
	}
	return false
}

func (m *Model) ensureVersionSpin() tea.Cmd {
	if m.versionSpinning || !m.anyVersionChecking() {
		return nil
	}
	m.versionSpinning = true
	m.versionSpinGen++
	if m.versionClock.IsZero() {
		m.versionClock = time.Now()
	}
	return m.versionTick()
}

func (m *Model) versionTick() tea.Cmd {
	gen := m.versionSpinGen
	return tea.Tick(installSpinInterval, func(at time.Time) tea.Msg {
		return ui.PageResultMsg{Page: ui.PageWebGUI, Result: versionSpinTickMsg{at: at, gen: gen}}
	})
}

type versionSpinTickMsg struct {
	at  time.Time
	gen uint64
}

// latestLabel describes the version check and update availability independently of progress.
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
		return latest + " · Update available"
	}
	return latest
}
