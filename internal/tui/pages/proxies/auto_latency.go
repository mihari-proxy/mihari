package proxies

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type delayTask struct {
	cancel               context.CancelFunc
	automatic, cancelled bool
	previous             DelayState
}

type delayIdentity struct {
	epoch        uint64
	pid          int
	started      time.Time
	restarts     uint64
	subscription string
}

// SetContextFactory binds test commands to the TUI owner's lifetime.
func (m *Model) SetContextFactory(factory func() (context.Context, context.CancelFunc)) {
	m.contextFactory = factory
}

// Stop cancels all commands owned by this page when the TUI exits.
func (m *Model) Stop() { m.cancelTests(false) }

// SetObscured suspends new visibility discovery while an overlay covers the page.
// Existing work and the current visit's seen set remain intact.
func (m *Model) SetObscured(obscured bool) {
	if m.autoObscured != obscured {
		m.autoDirty = true
	}
	m.autoObscured = obscured
}

func (m *Model) cancelTests(automaticOnly bool) {
	queue := m.queue[:0]
	for _, name := range m.queue {
		if automaticOnly && !m.autoQueued[name] {
			queue = append(queue, name)
		} else {
			delete(m.autoQueued, name)
		}
	}
	m.queue = queue
	for name, task := range m.delayTasks {
		if task.cancelled || (automaticOnly && !task.automatic) {
			continue
		}
		task.cancelled = true
		task.cancel()
		// Canceled work has no new result; avoid leaving an orphaned spinner.
		m.delays[name] = task.previous
	}
}

// ReconcileAutoTests schedules newly visible targets after shell state changes.
// Visibility never starts IO from View; all state changes remain on Update's owner.
func (m *Model) ReconcileAutoTests(active, ready bool, epoch uint64, core protocol.CoreStatus) (command tea.Cmd) {
	if m.concurrencyChanged {
		m.concurrencyChanged = false
		// Apply cancellation/identity changes first, then fill already queued
		// work even when automatic discovery is disabled or obscured.
		defer func() { command = tea.Batch(command, m.delayCmds()) }()
	}
	identity := delayIdentity{epoch, core.PID, core.StartedAt, core.Restarts, m.groupsSubscription}
	changed := m.autoIdentity != identity
	if changed {
		m.cancelTests(false)
		m.autoIdentity = identity
	}
	enabled := active && ready && m.preferences.AutoLatencyTest
	if changed || enabled != m.autoEnabled {
		if !enabled {
			m.cancelTests(true)
		}
		m.autoSeen = make(map[string]bool)
		m.autoDirty = true
		m.autoEnabled = enabled
	}
	if !enabled || m.autoObscured || !m.groupsFresh || m.loadError != "" || m.width <= 0 || m.height <= 0 || m.client == nil {
		return nil
	}
	if !m.autoDirty {
		return nil
	}
	m.autoDirty = false
	var candidates []string
	if m.routing.available && m.routing.known {
		candidates = append(candidates, m.routing.status.GlobalSelection)
	}
	m.buildVisibleContent(false, &candidates)
	for _, candidate := range candidates {
		leaf := m.selectedLeaf(candidate)
		if leaf == "" || m.autoSeen[leaf] {
			continue
		}
		m.autoSeen[leaf] = true
		if task := m.delayTasks[leaf]; task != nil && !task.cancelled {
			continue
		}
		queued := false
		for _, name := range m.queue {
			if name == leaf {
				queued = true
				break
			}
		}
		if !queued {
			m.queue = append(m.queue, leaf)
			m.autoQueued[leaf] = true
		}
	}
	return m.delayCmds()
}

func proxyResult(result tea.Msg) tea.Msg {
	return ui.PageResultMsg{Page: ui.PageProxies, Result: result}
}
