package runtime

import "github.com/LeeShunEE/mihari/internal/control/protocol"

func (m *Manager) Capabilities() []string {
	capabilities := []string{
		protocol.CapabilityCore,
		protocol.CapabilityProxies,
		protocol.CapabilityConnections,
		protocol.CapabilityRules,
		protocol.CapabilityLogs,
		protocol.CapabilitySubscriptions,
	}
	if m.preferences != nil {
		capabilities = append(capabilities, protocol.CapabilityPreferences)
	}
	if m.geoip != nil {
		capabilities = append(capabilities, protocol.CapabilityGeoIP)
	}
	return capabilities
}
