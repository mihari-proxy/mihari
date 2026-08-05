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
	ServiceInstall          = ui.ActionServiceInstall
	ServiceUninstall        = ui.ActionServiceUninstall
	ServiceStart            = ui.ActionServiceStart
	ServiceStop             = ui.ActionServiceStop
	ServiceRestart          = ui.ActionServiceRestart
	EnableSystemProxy       = ui.ActionEnableSystemProxy
	ForceSystemProxy        = ui.ActionForceSystemProxy
	DisableSystemProxy      = ui.ActionDisableSystemProxy
	EnableTun               = ui.ActionEnableTun
	DisableTun              = ui.ActionDisableTun
)

func RequiresConfirmation(action Action) bool {
	switch action {
	case DeleteSubscription, CloseAllConnections, UpdateAllProviders, RefreshAllSubscriptions, RollbackPanel, RestartCore, UpdateCore, ApplyEndpointChange,
		ServiceInstall, ServiceUninstall, ServiceStart, ServiceStop, ServiceRestart,
		EnableSystemProxy, ForceSystemProxy, DisableSystemProxy, EnableTun, DisableTun:
		return true
	default:
		return false
	}
}

// RequiresDaemon reports whether the action needs a live daemon control session.
// OS service control talks to the local service manager and works while disconnected.
func RequiresDaemon(action Action) bool {
	switch action {
	case ServiceInstall, ServiceUninstall, ServiceStart, ServiceStop, ServiceRestart:
		return false
	default:
		return true
	}
}

func knownAction(action Action) bool {
	switch action {
	case DeleteSubscription, CloseAllConnections, UpdateAllProviders, RefreshAllSubscriptions, RollbackPanel, RestartCore, UpdateCore, ApplyEndpointChange,
		SelectProxy, CloseConnection, RefreshSubscription, UpdateProvider,
		InstallPanel, UpdatePanel, ActivatePanel, OpenWebGUI,
		ServiceInstall, ServiceUninstall, ServiceStart, ServiceStop, ServiceRestart,
		EnableSystemProxy, ForceSystemProxy, DisableSystemProxy, EnableTun, DisableTun:
		return true
	default:
		return false
	}
}
