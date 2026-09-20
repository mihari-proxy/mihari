package webgui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

const installSpinInterval = 100 * time.Millisecond

type installSpinTickMsg struct {
	at  time.Time
	gen uint64
}

func (m *Model) beginInstall(pending ui.ActionPendingMsg) tea.Cmd {
	if pending.Page != ui.PageWebGUI {
		return nil
	}
	var prefix string
	switch pending.Action {
	case ui.ActionInstallPanel:
		prefix = "panel:install:"
	case ui.ActionReinstallPanel:
		prefix = "panel:reinstall:"
	case ui.ActionUpdatePanel:
		prefix = "panel:update:"
	default:
		return nil
	}
	if !strings.HasPrefix(pending.Key, prefix) || len(pending.Key) == len(prefix) {
		return nil
	}
	wasIdle := len(m.installing) == 0
	if m.installing == nil {
		m.installing = make(map[string]bool)
	}
	m.installing[pending.Key] = true
	if !wasIdle {
		return nil
	}
	m.installClock = time.Now()
	m.installSpinGen++
	return m.installTick()
}

func (m *Model) installTick() tea.Cmd {
	gen := m.installSpinGen
	return tea.Tick(installSpinInterval, func(at time.Time) tea.Msg {
		return ui.PageResultMsg{Page: ui.PageWebGUI, Result: installSpinTickMsg{at: at, gen: gen}}
	})
}
