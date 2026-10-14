package setup

import (
	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func (m *Model) setupUpgradeRequired() tea.Cmd {
	m.loading = false
	return m.localFailure("Setup requires an updated daemon", protocol.APIError{
		Code:    protocol.CodeInvalidState,
		Message: "Update and restart the daemon before continuing setup. Your input is kept.",
	})
}
