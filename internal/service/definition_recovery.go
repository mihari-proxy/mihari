package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
)

// DefinitionFileState is the content identity recorded before a file action.
func DefinitionFileState(file DefinitionFile, link string) string {
	if link != "" {
		return "link:" + link
	}
	metadata := make([]byte, 8+len(file.Bytes))
	binary.BigEndian.PutUint32(metadata[:4], file.Owner)
	binary.BigEndian.PutUint32(metadata[4:8], file.Mode)
	copy(metadata[8:], file.Bytes)
	sum := sha256.Sum256(metadata)
	return hex.EncodeToString(sum[:])
}
func observeDefinitionFile(ctx context.Context, store DefinitionStore, name string) (string, error) {
	file, err := store.Read(ctx, name)
	if errors.Is(err, os.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	if file.Kind == "mask" || file.Kind == "link" {
		link, err := store.ReadLink(ctx, name)
		if err != nil {
			return "", err
		}
		return DefinitionFileState(file, link), nil
	}
	return DefinitionFileState(file, ""), nil
}
func replayDefinitionFile(ctx context.Context, store DefinitionStore, action DefinitionAction) error {
	if action.NewState == "absent" {
		return store.Remove(ctx, action.Path)
	}
	if action.Link != "" {
		return store.Mask(ctx, action.Path, action.Link)
	}
	if action.File == nil || action.File.Path != action.Path {
		return invalidServiceState("missing definition candidate")
	}
	return store.Write(ctx, *action.File)
}
func (a *SystemdAdapter) ObserveAction(ctx context.Context, action DefinitionAction) (string, error) {
	if action.Path != "" {
		return observeDefinitionFile(ctx, a.files, action.Path)
	}
	if action.Kind == DefinitionActionReload {
		return action.OldState, nil
	}
	def, err := a.InspectDefinition(ctx)
	if err != nil {
		return "", err
	}
	switch action.Kind {
	case DefinitionActionStart, DefinitionActionStop:
		if def.Running {
			return "running", nil
		}
		return "stopped", nil
	}
	return "", invalidServiceState("unknown recorded service action")
}
func (a *SystemdAdapter) ReplayAction(ctx context.Context, action DefinitionAction) error {
	if action.Path != "" {
		return replayDefinitionFile(ctx, a.files, action)
	}
	switch action.Kind {
	case DefinitionActionReload:
		result, err := runAbsolute(ctx, a.runner, a.reloadArgv())
		if err != nil {
			return err
		}
		return requireZeroExit(result)
	case DefinitionActionStop:
		return a.stopUnit(ctx)
	case DefinitionActionStart:
		result, err := runAbsolute(ctx, a.runner, []string{a.paths.Systemctl, "--system", "start", "--", serviceUnitName})
		if err != nil {
			return err
		}
		return requireZeroExit(result)
	}
	return invalidServiceState("unknown recorded service action")
}
func (a *LaunchdAdapter) ObserveAction(ctx context.Context, action DefinitionAction) (string, error) {
	if action.Path != "" {
		return observeDefinitionFile(ctx, a.files, action.Path)
	}
	if action.Kind == DefinitionActionReload {
		return action.OldState, nil
	}
	switch action.Kind {
	case DefinitionActionStop:
		_, loaded, err := a.checkStopAuthority(ctx)
		if err != nil {
			return "", err
		}
		if loaded {
			return "loaded", nil
		}
		if err := a.WaitOwnedTreeExit(ctx); err != nil {
			return "", err
		}
		return "unloaded", nil
	case DefinitionActionStart:
		result, err := runAbsolute(ctx, a.runner, []string{a.paths.Launchctl, "print", a.jobTarget()})
		if err != nil {
			return "", err
		}
		loaded, _, _, err := parseLaunchdPrint(result, a.jobTarget())
		if err != nil {
			return "", err
		}
		if loaded {
			return "loaded", nil
		}
		return "unloaded", nil
	case DefinitionActionDisabled, DefinitionActionEnable, DefinitionActionDisable:
		result, err := runAbsolute(ctx, a.runner, []string{a.paths.Launchctl, "print-disabled", a.paths.Domain})
		if err != nil {
			return "", err
		}
		if err := requireZeroExit(result); err != nil {
			return "", err
		}
		disabled, err := parsePrintDisabled(result.Stdout, a.paths.Label)
		if err != nil {
			return "", err
		}
		if disabled {
			return "disabled", nil
		}
		return "enabled", nil
	}
	return "", invalidServiceState("unknown recorded service action")
}
func (a *LaunchdAdapter) ReplayAction(ctx context.Context, action DefinitionAction) error {
	if action.Path != "" {
		return replayDefinitionFile(ctx, a.files, action)
	}
	var args []string
	switch action.Kind {
	case DefinitionActionReload:
		return nil
	case DefinitionActionStop:
		if err := a.bootout(ctx); err != nil {
			return err
		}
		return a.WaitOwnedTreeExit(ctx)
	case DefinitionActionStart:
		args = []string{a.paths.Launchctl, "bootstrap", a.paths.Domain, a.paths.Plist}
	case DefinitionActionEnable, DefinitionActionDisable, DefinitionActionDisabled:
		verb := "disable"
		if action.NewState == "enabled" {
			verb = "enable"
		}
		args = []string{a.paths.Launchctl, verb, a.jobTarget()}
	default:
		return invalidServiceState("unknown recorded service action")
	}
	result, err := runAbsolute(ctx, a.runner, args)
	if err != nil {
		return err
	}
	return requireZeroExit(result)
}
