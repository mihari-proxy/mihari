package app

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/service"
)

// lifecycleActions borrows a prepared native journal. It changes only the
// manager definition and process state; the data and binary are retained.
func (x *InstallTransaction) lifecycleActions(ctx context.Context, operation string) error {
	if x.serviceEffects == nil || x.preparedAuthority == nil {
		return unknownInstallState()
	}
	switch operation {
	case "start", "stop", "restart", "uninstall":
	default:
		return invalidInstallRequest()
	}
	if operation != "start" {
		if err := x.Service.DisableAutostartAndStop(ctx); err != nil {
			return err
		}
		if err := x.Service.WaitOwnedTreeExit(ctx); err != nil {
			return err
		}
	}
	if err := x.activate(ctx); err != nil {
		return err
	}
	if err := x.removeObsoleteDefinitions(ctx); err != nil {
		return err
	}
	if operation != "uninstall" {
		if err := x.Service.WriteDefinition(ctx, x.preparedAuthority.TargetDefinition); err != nil {
			return err
		}
	} else if err := x.reloadStep(ctx); err != nil {
		return err
	}
	if x.preparedAuthority.TargetDefinition.Running {
		if err := x.Service.Start(ctx); err != nil {
			return err
		}
	}
	return x.setPhase(ctx, InstallPhaseComplete)
}

func (x *InstallTransaction) removeObsoleteDefinitions(ctx context.Context) error {
	if x.serviceEffects == nil || x.preparedAuthority == nil {
		return nil
	}
	target := x.preparedAuthority.TargetDefinition
	keep := map[string]bool{}
	for _, file := range target.Files {
		keep[file.Path] = true
	}
	for _, link := range target.Links {
		if target.Enabled || (target.Masked && link.Target == "/dev/null") {
			keep[link.Path] = true
		}
	}
	old := x.preparedAuthority.OldDefinition
	remove := func(path, kind string) error {
		if keep[path] {
			return nil
		}
		action := service.DefinitionAction{Kind: kind, TargetRole: JournalRoleDefinition, Path: path, NewState: "absent"}
		return x.journaledHook(ctx, action, func(ctx context.Context) error { return x.serviceEffects.adapter.ReplayAction(ctx, action) })
	}
	for _, file := range old.Files {
		kind := service.DefinitionActionDefinition
		if file.Kind == "dropin" {
			kind = service.DefinitionActionDropin
		}
		if err := remove(file.Path, kind); err != nil {
			return err
		}
	}
	for _, link := range old.Links {
		if err := remove(link.Path, service.DefinitionActionDisable); err != nil {
			return err
		}
	}
	return nil
}
