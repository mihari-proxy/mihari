package runtime

import "github.com/mihari-proxy/mihari/internal/control/protocol"

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
	// TUN is always advertised; live apply depends on the mihomo controller.
	capabilities = append(capabilities, protocol.CapabilityTUN)
	return capabilities
}
