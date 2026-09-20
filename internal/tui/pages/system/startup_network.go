package system

import (
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// SyncStartupNetwork schedules animation after the root publishes daemon status.
// Background application does not participate in manual action pending state.
func (m *Model) SyncStartupNetwork() tea.Cmd {
	return m.rowSpinCmdIfNeeded()
}

func (m *Model) startupApplying(rowID string) bool {
	if !m.mutationsEnabled || m.status.StartupNetwork == nil {
		return false
	}
	switch rowID {
	case rowSystemProxy:
		return m.hasCapability(protocol.CapabilitySystemProxy) && m.status.StartupNetwork.SystemProxyApplying
	case rowTUN:
		return m.hasCapability(protocol.CapabilityTUN) && m.status.StartupNetwork.TunApplying
	default:
		return false
	}
}

func (m *Model) hasRowProgress() bool {
	return (m.pending && m.pendingRow != "") || m.startupApplying(rowSystemProxy) || m.startupApplying(rowTUN)
}

func (m *Model) withStartupBadge(value, rowID string) string {
	if !m.startupApplying(rowID) {
		return value
	}
	clock := m.rowSpinClock
	if clock.IsZero() {
		clock = time.Unix(0, 0)
	}
	badge := ui.RenderStatusChip(m.theme, ui.StatusChipPending, ui.SpinnerLabel(clock, "Applying…"))
	label := ui.TUNLabel
	if rowID == rowSystemProxy {
		label = ui.SystemProxyLabel
	}
	// Reserve the badge before clipping a long observation on narrow pages.
	// The complete observation remains available in the row's detail view.
	width := ui.SectionTextWidth(ui.FullSectionInner(m.layoutWidth()))
	budget := max(0, width-lipgloss.Width(ui.FocusMarker+label)-4-lipgloss.Width(badge))
	return ui.TruncateVisible(value, budget) + "  " + badge
}
