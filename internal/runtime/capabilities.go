package runtime

import "github.com/LeeShunEE/mihari/internal/control/protocol"

func (m *Manager) Capabilities() []string {
	capabilities := []string{
		protocol.CapabilityCore,
		protocol.CapabilityProxies,
		protocol.CapabilityConnections,
		protocol.CapabilityRules,
		protocol.CapabilityRuleProviders,
		protocol.CapabilityLogs,
		protocol.CapabilitySubscriptions,
	}
	if m.preferences != nil {
		capabilities = append(capabilities, protocol.CapabilityPreferences)
	}
	if m.geoip != nil {
		capabilities = append(capabilities, protocol.CapabilityGeoIP)
	}
	if m.onboarding != nil {
		capabilities = append(capabilities, protocol.CapabilityOnboarding)
	}
	if m.webGateway != nil && m.panels != nil {
		capabilities = append(capabilities, protocol.CapabilityWebGUI)
	}
	if m.sysProxy != nil {
		capabilities = append(capabilities, protocol.CapabilitySystemProxy)
	}
	return capabilities
}
