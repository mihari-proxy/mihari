package system

import (
	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func (m *Model) confirmAllowLANToggle() tea.Cmd {
	if !m.mutationsEnabled || m.client == nil || !m.hasCapability(protocol.CapabilityOnboarding) {
		return nil
	}
	baseline := m.onboarding
	enable := !baseline.AllowLAN
	if err := ui.ValidateMixedLAN(baseline.MixedAddr, enable); err != nil {
		m.lastError = err.Error()
		return m.localFailure("allow-lan.validate", "Change Mixed to a loopback address before disabling Allow LAN", err)
	}
	impact := "Disable LAN access and restart the daemon. Active connections will be interrupted."
	if enable {
		impact = "Permit Mixed to listen on LAN addresses after daemon restart. The saved Mixed address is kept; 127.0.0.1 still accepts only local connections. Proxy authentication is unchanged. Active connections will be interrupted."
	}
	ctx, client := m.ctx, m.client
	request := protocol.OnboardingUpdateRequest{OperationID: m.newOperationID(), AllowLAN: &enable}
	return func() tea.Msg {
		return ui.ActionIntentMsg{
			Action: ui.ActionApplyEndpointChange, Page: ui.PageSystem, Capability: protocol.CapabilityOnboarding,
			Key: "system:allow-lan", Title: "Change Allow LAN", Object: "Mixed proxy listener",
			Impact: impact, Rollback: ui.ReplaceConfigurationRollback,
			Execute: func() tea.Msg {
				status, err := applyPortUpdate(ctx, client, baseline, request)
				return portsApplyResultMsg{rowID: rowAllowLAN, status: status, err: err}
			},
		}
	}
}
