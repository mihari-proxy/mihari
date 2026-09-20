package logs

import (
	"context"
	"crypto/rand"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// PreferenceClient saves display preferences through the daemon.
type PreferenceClient interface {
	UpdateTUIPreferences(context.Context, protocol.UpdateTUIPreferencesRequest) (protocol.TUIPreferences, error)
}

type preferenceState struct {
	client        PreferenceClient
	newContext    func() (context.Context, context.CancelFunc)
	cancel        context.CancelFunc
	stopped       bool
	loaded        bool
	version       uint64
	saving        bool
	savingVersion uint64
	unsaved       bool
	clock         time.Time
}

type preferenceSavedMsg struct {
	version uint64
	outcome protocol.WarningOutcome
	err     error
}

func (m preferenceSavedMsg) Err() error                        { return m.err }
func (m preferenceSavedMsg) Warnings() protocol.WarningOutcome { return m.outcome }
func (m preferenceSavedMsg) DiagnosticPage() ui.PageID         { return ui.PageLogs }

type preferenceTickMsg struct {
	version uint64
	at      time.Time
}

// SetPreferenceClient configures asynchronous saving and its owning lifetime.
func (m *Model) SetPreferenceClient(client PreferenceClient, newContext func() (context.Context, context.CancelFunc)) {
	m.preference.client = client
	if newContext == nil {
		newContext = func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		}
	}
	m.preference.newContext = newContext
}

// Stop cancels the active client request without waiting or flushing newer edits.
func (m *Model) Stop() {
	m.preference.stopped = true
	m.preference.saving = false
	if m.preference.cancel != nil {
		m.preference.cancel()
		m.preference.cancel = nil
	}
}

// SetPreferences restores the first snapshot unless this window has already edited its filter.
func (m *Model) SetPreferences(value protocol.TUIPreferences) {
	if m.preference.loaded || m.preference.version != 0 {
		return
	}
	levels := allLevels
	if value.LogLevels != nil {
		levels = 0
		for _, level := range value.LogLevels {
			bit := selectionForLevel(level)
			if bit == 0 || level == "" {
				return
			}
			levels |= bit
		}
		if levels == 0 {
			return
		}
	}
	m.preference.loaded = true
	m.levels = levels
	m.reconcileFocus()
}

func (m *Model) savePreference() tea.Cmd {
	state := &m.preference
	if state.client == nil || state.saving || state.stopped {
		return nil
	}
	state.saving, state.unsaved = true, false
	state.savingVersion = state.version
	state.clock = time.Now()
	version, client, newContext := state.version, state.client, state.newContext
	levels := make([]string, 0, len(selectableLevels))
	for i, level := range selectableLevels {
		if m.levels&(1<<i) != 0 {
			levels = append(levels, level)
		}
	}
	request := protocol.UpdateTUIPreferencesRequest{OperationID: "logs-level-" + rand.Text(), LogLevels: levels}
	parent, release := newContext()
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	stop := func() {
		cancel()
		release()
	}
	state.cancel = stop
	save := func() tea.Msg {
		defer stop()
		value, err := client.UpdateTUIPreferences(ctx, request)
		return ui.PageResultMsg{Page: ui.PageLogs, Result: preferenceSavedMsg{version: version, outcome: value.WarningOutcome, err: err}}
	}
	return tea.Batch(save, m.preferenceTick())
}

func (m *Model) preferenceTick() tea.Cmd {
	version := m.preference.savingVersion
	return tea.Tick(100*time.Millisecond, func(at time.Time) tea.Msg {
		return ui.PageResultMsg{Page: ui.PageLogs, Result: preferenceTickMsg{version: version, at: at}}
	})
}

func (m *Model) finishPreference(result preferenceSavedMsg) tea.Cmd {
	state := &m.preference
	if !state.saving || result.version != state.savingVersion {
		return nil
	}
	state.saving = false
	state.cancel = nil // The completed command has already released its context.
	if state.version != result.version {
		// Only a newer explicit edit can start another save. Failed requests
		// are never retried, and no copy of an unsaved preference is retained.
		return m.savePreference()
	}
	state.unsaved = result.err != nil
	return nil
}

func (m *Model) preferenceBadge() string {
	if m.preference.saving {
		return " " + ui.RenderStatusChip(m.theme, ui.StatusChipPending, ui.SpinnerLabel(m.preference.clock, "Saving…"))
	}
	if m.preference.unsaved {
		return " " + ui.RenderStatusChip(m.theme, ui.StatusChipFailed, "Unsaved")
	}
	return ""
}
