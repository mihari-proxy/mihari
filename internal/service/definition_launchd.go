package service

import (
	"context"
	"errors"
	"os"
	"strings"
)

// LaunchdPaths is the absolute launchd system-domain layout.
type LaunchdPaths struct {
	Launchctl string
	Plist     string
	Label     string
	Domain    string
}

// DefaultLaunchdPaths returns the production LaunchDaemons layout.
func DefaultLaunchdPaths() LaunchdPaths {
	return LaunchdPaths{
		Launchctl: defaultLaunchctl,
		Plist:     defaultPlistPath,
		Label:     serviceLabel,
		Domain:    "system",
	}
}

type launchdConfig struct {
	Runner CommandRunner
	Files  DefinitionStore
	Tree   ProcessTree
	Clock  Clock
	Hook   ActionHook
	Paths  LaunchdPaths
}

// LaunchdAdapter inspects and mutates the system LaunchDaemon.
type LaunchdAdapter struct {
	runner CommandRunner
	files  DefinitionStore
	tree   ProcessTree
	clock  Clock
	hook   ActionHook
	paths  LaunchdPaths
	last   Definition
}

func newLaunchdAdapter(cfg launchdConfig) *LaunchdAdapter {
	if cfg.Hook == nil {
		cfg.Hook = DirectActionHook
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.Paths.Launchctl == "" {
		cfg.Paths = DefaultLaunchdPaths()
	}
	if cfg.Paths.Domain == "" {
		cfg.Paths.Domain = "system"
	}
	return &LaunchdAdapter{
		runner: cfg.Runner,
		files:  cfg.Files,
		tree:   cfg.Tree,
		clock:  cfg.Clock,
		hook:   cfg.Hook,
		paths:  cfg.Paths,
	}
}

func (a *LaunchdAdapter) InspectDefinition(ctx context.Context) (Definition, error) {
	if err := ctx.Err(); err != nil {
		return Definition{}, err
	}
	if a.paths.Domain != "system" {
		return Definition{}, invalidServiceState("service status is unknown")
	}
	plist, plistErr := a.files.Read(ctx, a.paths.Plist)
	plistMissing := errors.Is(plistErr, os.ErrNotExist)
	if plistErr != nil && !plistMissing {
		return Definition{}, invalidServiceState("service status is unknown")
	}
	if !plistMissing && (plist.Kind == "link" || plist.Kind == "mask") {
		return Definition{}, invalidServiceState("service definition is unsupported")
	}

	printOut, err := runAbsolute(ctx, a.runner, []string{a.paths.Launchctl, "print", a.jobTarget()})
	if err != nil {
		return Definition{}, err
	}
	loaded, running, pid, err := parseLaunchdPrint(printOut, a.jobTarget())
	if err != nil {
		return Definition{}, err
	}
	if plistMissing && !loaded {
		def := Definition{Status: StatusNotInstalled}
		a.last = def
		return def, nil
	}
	if plistMissing && loaded {
		return Definition{}, invalidServiceState("service status is unknown")
	}

	disabledOut, err := runAbsolute(ctx, a.runner, []string{a.paths.Launchctl, "print-disabled", a.paths.Domain})
	if err != nil {
		return Definition{}, err
	}
	disabled, err := parsePrintDisabled(disabledOut.Stdout, a.paths.Label)
	if err != nil {
		return Definition{}, err
	}

	parsed, err := parseLaunchdPlist(plist.Bytes)
	if err != nil {
		return Definition{}, err
	}
	plist.Kind = "plist"
	def := Definition{
		Status:  definitionStatus(true, running && loaded),
		Enabled: !disabled,
		Running: running && loaded,
		Binary:  parsed.Binary,
		Args:    append([]string(nil), parsed.Args...),
		Env:     append([]string(nil), parsed.Env...),
		Files:   []DefinitionFile{plist},
		Process: ProcessIdentity{PID: pid},
	}
	if !loaded {
		def.Running = false
		def.Status = StatusStopped
		def.Process = ProcessIdentity{}
	} else if pid > 0 {
		id, err := a.identifyPID(ctx, pid)
		if err != nil {
			return Definition{}, err
		}
		def.Process = id
	}
	a.last = cloneDefinition(def)
	return cloneDefinition(def), nil
}

func (a *LaunchdAdapter) identifyPID(ctx context.Context, pid int) (ProcessIdentity, error) {
	if a.tree == nil {
		return ProcessIdentity{}, invalidServiceState("service process identity is unknown")
	}
	id, err := a.tree.Identify(ctx, pid)
	if err != nil {
		return ProcessIdentity{}, err
	}
	id.PID = pid
	if incompleteProcessIdentity(id) {
		return ProcessIdentity{}, invalidServiceState("service process identity is unknown")
	}
	return id, nil
}

func (a *LaunchdAdapter) DisableAutostartAndStop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.paths.Domain != "system" {
		return invalidServiceState("service status is unknown")
	}
	def, err := a.InspectDefinition(ctx)
	if err != nil {
		return err
	}
	if def.Status == StatusNotInstalled {
		return nil
	}
	err = applyAction(ctx, a.hook, DefinitionAction{
		Kind:       DefinitionActionDisabled,
		TargetRole: "definition",
		OldState:   "enabled",
		NewState:   "disabled",
	}, func(ctx context.Context) error {
		result, err := runAbsolute(ctx, a.runner, []string{a.paths.Launchctl, "disable", a.jobTarget()})
		if err != nil {
			return err
		}
		if err := requireZeroExit(result); err != nil {
			return err
		}
		result, err = runAbsolute(ctx, a.runner, []string{a.paths.Launchctl, "print-disabled", a.paths.Domain})
		if err != nil {
			return err
		}
		if err := requireZeroExit(result); err != nil {
			return err
		}
		disabled, err := parsePrintDisabled(result.Stdout, a.paths.Label)
		if err != nil {
			return err
		}
		if !disabled {
			return invalidServiceState("service status is unknown")
		}
		return nil
	})
	if err != nil {
		return err
	}
	return applyAction(ctx, a.hook, DefinitionAction{
		Kind:       DefinitionActionStop,
		TargetRole: "definition",
		OldState:   "loaded",
		NewState:   "unloaded",
	}, func(ctx context.Context) error {
		return a.bootout(ctx)
	})
}

func (a *LaunchdAdapter) bootout(ctx context.Context) error {
	result, err := runAbsolute(ctx, a.runner, []string{a.paths.Launchctl, "bootout", a.jobTarget()})
	if err != nil {
		return err
	}
	if result.ExitCode == 0 {
		return nil
	}
	loaded, _, _, parseErr := parseLaunchdPrint(result, a.jobTarget())
	if parseErr == nil && !loaded {
		return nil
	}
	return invalidServiceState("service manager query failed")
}

func (a *LaunchdAdapter) WaitOwnedTreeExit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if incompleteProcessIdentity(a.last.Process) {
		return invalidServiceState("service process identity is unknown")
	}
	id := a.last.Process
	if a.tree == nil {
		return invalidServiceState("service process identity is unknown")
	}
	alive, err := a.tree.Lookup(ctx, id)
	if err != nil {
		return err
	}
	if !alive {
		return nil
	}
	if err := a.tree.SignalIdentity(ctx, id, "TERM"); err != nil {
		return err
	}
	if err := waitUntil(ctx, a.clock, termWait, func(ctx context.Context) (bool, error) {
		alive, err := a.tree.Lookup(ctx, id)
		return !alive, err
	}); err != nil {
		return err
	}
	alive, err = a.tree.Lookup(ctx, id)
	if err != nil {
		return err
	}
	if !alive {
		return nil
	}
	if err := a.tree.SignalIdentity(ctx, id, "KILL"); err != nil {
		return err
	}
	if err := waitUntil(ctx, a.clock, killWait, func(ctx context.Context) (bool, error) {
		alive, err := a.tree.Lookup(ctx, id)
		return !alive, err
	}); err != nil {
		return err
	}
	alive, err = a.tree.Lookup(ctx, id)
	if err != nil {
		return err
	}
	if alive {
		return invalidServiceState("managed process tree did not exit")
	}
	return nil
}

func (a *LaunchdAdapter) WriteDefinition(ctx context.Context, def Definition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.paths.Domain != "system" {
		return invalidServiceState("service definition is unsupported")
	}
	if err := a.writeFiles(ctx, def.Files); err != nil {
		return err
	}
	if err := a.applyDisabled(ctx, !def.Enabled); err != nil {
		return err
	}
	a.last = cloneDefinition(def)
	return nil
}

func (a *LaunchdAdapter) RestoreDefinition(ctx context.Context, def Definition) error {
	return a.WriteDefinition(ctx, def)
}

func (a *LaunchdAdapter) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.paths.Domain != "system" {
		return invalidServiceState("service status is unknown")
	}
	enabled := a.last.Enabled
	if err := a.applyDisabled(ctx, false); err != nil {
		return err
	}
	err := applyAction(ctx, a.hook, DefinitionAction{
		Kind:       DefinitionActionStart,
		TargetRole: "definition",
		OldState:   "unloaded",
		NewState:   "loaded",
	}, func(ctx context.Context) error {
		result, err := runAbsolute(ctx, a.runner, []string{a.paths.Launchctl, "bootstrap", a.paths.Domain, a.paths.Plist})
		if err != nil {
			return err
		}
		return requireZeroExit(result)
	})
	if err != nil {
		return err
	}
	if enabled {
		return a.requireLaunchdRunning(ctx)
	}
	if err := a.applyDisabled(ctx, true); err != nil {
		return err
	}
	if err := a.requireLaunchdRunning(ctx); err != nil {
		return err
	}
	disabledOut, err := runAbsolute(ctx, a.runner, []string{a.paths.Launchctl, "print-disabled", a.paths.Domain})
	if err != nil {
		return err
	}
	if err := requireZeroExit(disabledOut); err != nil {
		return err
	}
	disabled, err := parsePrintDisabled(disabledOut.Stdout, a.paths.Label)
	if err != nil || !disabled {
		return invalidServiceState("service status is unknown")
	}
	return nil
}

func (a *LaunchdAdapter) requireLaunchdRunning(ctx context.Context) error {
	result, err := runAbsolute(ctx, a.runner, []string{a.paths.Launchctl, "print", a.jobTarget()})
	if err != nil {
		return err
	}
	loaded, running, _, err := parseLaunchdPrint(result, a.jobTarget())
	if err != nil || !loaded || !running {
		return invalidServiceState("service status is unknown")
	}
	return nil
}

func (a *LaunchdAdapter) Probe(ctx context.Context) (Definition, error) {
	return a.InspectDefinition(ctx)
}

func (a *LaunchdAdapter) applyDisabled(ctx context.Context, disabled bool) error {
	kind := DefinitionActionEnable
	oldState, newState := "disabled", "enabled"
	verb := "enable"
	if disabled {
		kind = DefinitionActionDisabled
		oldState, newState = "enabled", "disabled"
		verb = "disable"
	}
	return applyAction(ctx, a.hook, DefinitionAction{
		Kind:       kind,
		TargetRole: "definition",
		OldState:   oldState,
		NewState:   newState,
	}, func(ctx context.Context) error {
		result, err := runAbsolute(ctx, a.runner, []string{a.paths.Launchctl, verb, a.jobTarget()})
		if err != nil {
			return err
		}
		if err := requireZeroExit(result); err != nil {
			return err
		}
		result, err = runAbsolute(ctx, a.runner, []string{a.paths.Launchctl, "print-disabled", a.paths.Domain})
		if err != nil {
			return err
		}
		if err := requireZeroExit(result); err != nil {
			return err
		}
		got, err := parsePrintDisabled(result.Stdout, a.paths.Label)
		if err != nil {
			return err
		}
		if got != disabled {
			return invalidServiceState("service status is unknown")
		}
		return nil
	})
}

func (a *LaunchdAdapter) writeFiles(ctx context.Context, files []DefinitionFile) error {
	for _, file := range files {
		file := file
		err := applyAction(ctx, a.hook, DefinitionAction{
			Kind: DefinitionActionDefinition,
			Path: file.Path, File: &file,
			TargetRole: "definition",
			OldState:   "absent",
			NewState:   "present",
		}, func(ctx context.Context) error {
			if strings.Contains(file.Path, "LaunchAgents") {
				return invalidServiceState("service definition is unsupported")
			}
			return a.files.Write(ctx, file)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *LaunchdAdapter) jobTarget() string {
	return a.paths.Domain + "/" + a.paths.Label
}
