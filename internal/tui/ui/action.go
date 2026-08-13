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
	ActionSwitchCoreChannel       Action = "switch-core-channel"
	ActionUpdateMihari            Action = "update-mihari"
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
	ActionUninstallPanel          Action = "uninstall-panel"
	ActionReinstallPanel          Action = "reinstall-panel"
	ActionServiceInstall          Action = "service-install"
	ActionServiceUninstall        Action = "service-uninstall"
	ActionServiceReinstall        Action = "service-reinstall"
	ActionServiceStart            Action = "service-start"
	ActionServiceStop             Action = "service-stop"
	ActionServiceRestart          Action = "service-restart"
	ActionEnableSystemProxy       Action = "enable-system-proxy"
	ActionForceSystemProxy        Action = "force-system-proxy"
	ActionDisableSystemProxy      Action = "disable-system-proxy"
	ActionEnableTun               Action = "enable-tun"
	ActionDisableTun              Action = "disable-tun"
	ActionForceTun                Action = "force-tun"
)

// RelaunchRequestMsg asks the root shell to exit and enter the replacement TUI.
// Warning must already be sanitized for display after terminal restoration.
type RelaunchRequestMsg struct {
	Warning string
}

// PageResultMsg routes asynchronous page-owned work back to its originating page.
type PageResultMsg struct {
	Page   PageID
	Result tea.Msg
}

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

// ActionPendingMsg is delivered to the target page when a confirmed action begins
// executing, so pages can show row-local progress (braille + note) before the result.
type ActionPendingMsg struct {
	Page   PageID
	Action Action
	Key    string
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
