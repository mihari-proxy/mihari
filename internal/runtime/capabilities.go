package runtime

import "github.com/LeeShunEE/mihari/internal/control/protocol"

func (m *Manager) Capabilities() []string {
	return []string{
		protocol.CapabilityCore,
		protocol.CapabilityProxies,
		protocol.CapabilityConnections,
		protocol.CapabilityRules,
		protocol.CapabilityLogs,
		protocol.CapabilitySubscriptions,
	}
}
