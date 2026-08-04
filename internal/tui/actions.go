package tui

import "github.com/LeeShunEE/mihari/internal/tui/ui"

type Action = ui.Action

const (
	DeleteSubscription      = ui.ActionDeleteSubscription
	CloseAllConnections     = ui.ActionCloseAllConnections
	UpdateAllProviders      = ui.ActionUpdateAllProviders
	RollbackPanel           = ui.ActionRollbackPanel
	RestartCore             = ui.ActionRestartCore
	UpdateCore              = ui.ActionUpdateCore
	ApplyEndpointChange     = ui.ActionApplyEndpointChange
	SelectProxy             = ui.ActionSelectProxy
	CloseConnection         = ui.ActionCloseConnection
	RefreshSubscription     = ui.ActionRefreshSubscription
	RefreshAllSubscriptions = ui.ActionRefreshAllSubscriptions
	UpdateProvider          = ui.ActionUpdateProvider
	InstallPanel            = ui.ActionInstallPanel
	UpdatePanel             = ui.ActionUpdatePanel
	ActivatePanel           = ui.ActionActivatePanel
	OpenWebGUI              = ui.ActionOpenWebGUI
)

func RequiresConfirmation(action Action) bool {
	switch action {
	case DeleteSubscription, CloseAllConnections, UpdateAllProviders, RefreshAllSubscriptions, RollbackPanel, RestartCore, UpdateCore, ApplyEndpointChange:
		return true
	default:
		return false
	}
}

func knownAction(action Action) bool {
	switch action {
	case DeleteSubscription, CloseAllConnections, UpdateAllProviders, RefreshAllSubscriptions, RollbackPanel, RestartCore, UpdateCore, ApplyEndpointChange,
		SelectProxy, CloseConnection, RefreshSubscription, UpdateProvider,
		InstallPanel, UpdatePanel, ActivatePanel, OpenWebGUI:
		return true
	default:
		return false
	}
}
