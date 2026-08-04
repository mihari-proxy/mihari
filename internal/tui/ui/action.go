package ui

import tea "charm.land/bubbletea/v2"

type Action string

const (
	ActionDeleteSubscription      Action = "delete-subscription"
	ActionCloseAllConnections     Action = "close-all-connections"
	ActionUpdateAllProviders      Action = "update-all-providers"
	ActionRollbackPanel           Action = "rollback-panel"
	ActionRestartCore             Action = "restart-core"
	ActionUpdateCore              Action = "update-core"
	ActionApplyEndpointChange     Action = "apply-endpoint-change"
	ActionSelectProxy             Action = "select-proxy"
	ActionCloseConnection         Action = "close-connection"
	ActionRefreshSubscription     Action = "refresh-subscription"
	ActionRefreshAllSubscriptions Action = "refresh-all-subscriptions"
	ActionUpdateProvider          Action = "update-provider"
	ActionInstallPanel            Action = "install-panel"
	ActionUpdatePanel             Action = "update-panel"
	ActionActivatePanel           Action = "activate-panel"
	ActionOpenWebGUI              Action = "open-web-gui"
)

type ActionIntentMsg struct {
	Action     Action
	Page       PageID
	Capability string
	Key        string
	Title      string
	Object     string
	Impact     string
	Rollback   string
	Execute    tea.Cmd
}

type GlobalState string

const (
	StatePending          GlobalState = "Pending"
	StateStale            GlobalState = "Stale"
	StateRevisionConflict GlobalState = "RevisionConflict"
	StateCapabilityLost   GlobalState = "CapabilityLost"
	StateReconnected      GlobalState = "Reconnected"
)

type GlobalStateMsg struct {
	State  GlobalState
	Action Action
	Key    string
}

// GlobalStateLabel maps a dispatcher state to the footer toast shown to the
// user. It returns an empty string when no toast should be rendered.
func GlobalStateLabel(state GlobalState) string {
	switch state {
	case StatePending:
		return GlobalStatePendingLabel
	case StateStale:
		return GlobalStateStaleLabel
	case StateCapabilityLost:
		return GlobalStateCapabilityLostLabel
	case StateReconnected:
		return GlobalStateReconnectedLabel
	case StateRevisionConflict:
		return GlobalStateConflictLabel
	default:
		return ""
	}
}
