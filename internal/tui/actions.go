package tui

import "github.com/mihari-proxy/mihari/internal/tui/ui"

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
	UninstallPanel          = ui.ActionUninstallPanel
	ReinstallPanel          = ui.ActionReinstallPanel
	ServiceInstall          = ui.ActionServiceInstall
	ServiceUninstall        = ui.ActionServiceUninstall
	ServiceReinstall        = ui.ActionServiceReinstall
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
		UninstallPanel, ReinstallPanel,
		ServiceInstall, ServiceUninstall, ServiceReinstall, ServiceStart, ServiceStop, ServiceRestart,
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
	case ServiceInstall, ServiceUninstall, ServiceReinstall, ServiceStart, ServiceStop, ServiceRestart:
		return false
	default:
		return true
	}
}

func knownAction(action Action) bool {
	switch action {
	case DeleteSubscription, CloseAllConnections, UpdateAllProviders, RefreshAllSubscriptions, RollbackPanel, RestartCore, UpdateCore, ApplyEndpointChange,
		SelectProxy, CloseConnection, RefreshSubscription, UpdateProvider,
		InstallPanel, UpdatePanel, ActivatePanel, OpenWebGUI, UninstallPanel, ReinstallPanel,
		ServiceInstall, ServiceUninstall, ServiceReinstall, ServiceStart, ServiceStop, ServiceRestart,
		EnableSystemProxy, ForceSystemProxy, DisableSystemProxy, EnableTun, DisableTun:
		return true
	default:
		return false
	}
}
