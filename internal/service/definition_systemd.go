package service

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// SystemdPaths is the absolute systemd layout used by the Linux adapter.
type SystemdPaths struct {
	Systemctl string
	UnitDir   string
	UnitFile  string
	DropinDir string
	WantsLink string
	DevNull   string
}

// DefaultSystemdPaths returns the production /etc unit layout.
func DefaultSystemdPaths() SystemdPaths {
	return SystemdPaths{
		Systemctl: defaultSystemctl,
		UnitDir:   defaultSystemdUnitDir,
		UnitFile:  defaultSystemdUnitFile,
		DropinDir: defaultSystemdDropinDir,
		WantsLink: defaultSystemdUnitDir + "/multi-user.target.wants/" + serviceUnitName,
		DevNull:   defaultDevNull,
	}
}

// SystemdConfig injects the OS boundaries for an explicitly assembled adapter.
type SystemdConfig struct {
	Runner CommandRunner
	Files  DefinitionStore
	Tree   ProcessTree
	Clock  Clock
	Hook   ActionHook
	Paths  SystemdPaths
}

// SystemdAdapter inspects and mutates the system mihari.service unit.
type SystemdAdapter struct {
	runner CommandRunner
	files  DefinitionStore
	tree   ProcessTree
	clock  Clock
	hook   ActionHook
	paths  SystemdPaths
	last   Definition
}

type systemdConfig = SystemdConfig

// NewSystemdAdapterWithConfig constructs an adapter from explicit OS boundaries.
func NewSystemdAdapterWithConfig(cfg SystemdConfig) *SystemdAdapter { return newSystemdAdapter(cfg) }

func newSystemdAdapter(cfg systemdConfig) *SystemdAdapter {
	if cfg.Hook == nil {
		cfg.Hook = DirectActionHook
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.Paths.Systemctl == "" {
		cfg.Paths = DefaultSystemdPaths()
	}
	if configured, ok := cfg.Files.(interface {
		withSystemdPaths(SystemdPaths) DefinitionStore
	}); ok {
		cfg.Files = configured.withSystemdPaths(cfg.Paths)
	}
	return &SystemdAdapter{
		runner: cfg.Runner,
		files:  cfg.Files,
		tree:   cfg.Tree,
		clock:  cfg.Clock,
		hook:   cfg.Hook,
		paths:  cfg.Paths,
	}
}

func (a *SystemdAdapter) InspectDefinition(ctx context.Context) (Definition, error) {
	if err := ctx.Err(); err != nil {
		return Definition{}, err
	}
	result, err := runAbsolute(ctx, a.runner, a.showArgv())
	if err != nil {
		return Definition{}, err
	}
	props, err := parseSystemdShow(result.Stdout)
	if err != nil {
		return Definition{}, err
	}
	def, err := a.definitionFromShow(ctx, props)
	if err != nil {
		return Definition{}, err
	}
	a.last = cloneDefinition(def)
	return cloneDefinition(def), nil
}

func (a *SystemdAdapter) definitionFromShow(ctx context.Context, props map[string]string) (Definition, error) {
	load := props["LoadState"]
	active := props["ActiveState"]
	fragment := props["FragmentPath"]
	dropins := strings.Fields(props["DropInPaths"])
	unitFileState := props["UnitFileState"]

	unit, unitErr := a.files.Read(ctx, a.paths.UnitFile)
	unitMissing := errors.Is(unitErr, os.ErrNotExist)
	if unitErr != nil && !unitMissing {
		return Definition{}, invalidServiceState("service status is unknown")
	}

	if load == "not-found" && unitMissing {
		return Definition{Status: StatusNotInstalled}, nil
	}
	switch load {
	case "loaded", "masked":
	default:
		return Definition{}, invalidServiceState("service status is unknown")
	}

	running := false
	switch active {
	case "active":
		running = true
	case "inactive", "failed":
		running = false
	default:
		return Definition{}, invalidServiceState("service status is unknown")
	}

	masked := load == "masked" || unit.Kind == "mask"
	if masked {
		target, err := a.files.ReadLink(ctx, a.paths.UnitFile)
		if err == nil && target != a.paths.DevNull {
			return Definition{}, invalidServiceState("service status is unknown")
		}
		if fragment != defaultDevNull && fragment != a.paths.UnitFile {
			return Definition{}, invalidServiceState("service status is unknown")
		}
	} else if fragment != a.paths.UnitFile {
		return Definition{}, invalidServiceState("service status is unknown")
	}

	for _, dropin := range dropins {
		if !allowedDropin(a.paths.DropinDir, dropin) {
			return Definition{}, invalidServiceState("service definition is unsupported")
		}
	}
	listed, err := a.files.List(ctx, a.paths.DropinDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Definition{}, invalidServiceState("service status is unknown")
	}
	listedSet := map[string]struct{}{}
	for _, name := range listed {
		if !strings.HasSuffix(name, ".conf") {
			continue
		}
		listedSet[name] = struct{}{}
		if !containsString(dropins, name) {
			return Definition{}, invalidServiceState("service definition is unsupported")
		}
	}
	for _, dropin := range dropins {
		if _, ok := listedSet[dropin]; !ok {
			file, readErr := a.files.Read(ctx, dropin)
			if readErr != nil {
				return Definition{}, invalidServiceState("service definition is unsupported")
			}
			_ = file
		}
	}

	def := Definition{
		Masked:  masked,
		Running: running,
		Enabled: unitFileState == "enabled" && !masked,
		Status:  definitionStatus(true, running),
	}
	if !unitMissing && unit.Kind == "mask" {
		def.Links = append(def.Links, DefinitionLink{Identity: unit.Identity, Path: a.paths.UnitFile, Target: a.paths.DevNull, Owner: unit.Owner, Mode: unit.Mode})
	}
	if !unitMissing && unit.Kind != "mask" {
		parsed, err := parseSystemdUnitFile(unit.Bytes)
		if err != nil {
			return Definition{}, err
		}
		if parsed.Binary == "" {
			return Definition{}, invalidServiceState("service definition is unsupported")
		}
		for _, envFile := range parsed.envFiles {
			if err := a.checkEnvFile(ctx, envFile); err != nil {
				return Definition{}, err
			}
		}
		unit.Kind = "unit"
		def.Binary = parsed.Binary
		def.Args = append([]string(nil), parsed.Args...)
		def.Env = append([]string(nil), parsed.Env...)
		def.Files = append(def.Files, unit)
	}
	for _, dropinPath := range dropins {
		file, err := a.files.Read(ctx, dropinPath)
		if err != nil {
			return Definition{}, invalidServiceState("service definition is unsupported")
		}
		parsed, err := parseSystemdUnitFile(file.Bytes)
		if err != nil {
			return Definition{}, err
		}
		if parsed.Binary != "" || parsed.execKeys > 0 {
			return Definition{}, invalidServiceState("service definition is unsupported")
		}
		file.Kind = "dropin"
		def.Files = append(def.Files, file)
	}

	if link, err := a.files.ReadLink(ctx, a.paths.WantsLink); err == nil {
		info, readErr := a.files.Read(ctx, a.paths.WantsLink)
		if readErr != nil {
			return Definition{}, invalidServiceState("service status is unknown")
		}
		def.Links = append(def.Links, DefinitionLink{Identity: info.Identity, Path: a.paths.WantsLink, Target: link, Owner: info.Owner, Mode: info.Mode})
		if !masked {
			def.Enabled = true
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Definition{}, invalidServiceState("service status is unknown")
	}

	pid, err := strconv.Atoi(props["MainPID"])
	if err != nil || pid < 0 {
		return Definition{}, invalidServiceState("service status is unknown")
	}
	def.Process = ProcessIdentity{PID: pid, Group: props["ControlGroup"]}
	if running && (pid == 0 || def.Process.Group == "") {
		return Definition{}, invalidServiceState("service status is unknown")
	}
	if pid > 0 {
		id, err := a.identifyPID(ctx, pid)
		if err != nil {
			return Definition{}, err
		}
		id.Group = props["ControlGroup"]
		def.Process = id
	}
	return def, nil
}

func (a *SystemdAdapter) identifyPID(ctx context.Context, pid int) (ProcessIdentity, error) {
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

func (a *SystemdAdapter) checkEnvFile(ctx context.Context, spec string) error {
	optional := strings.HasPrefix(spec, "-")
	name := strings.TrimPrefix(spec, "-")
	if !unixAbs(name) {
		return invalidServiceState("service definition is unsupported")
	}
	_, err := a.files.Read(ctx, name)
	if errors.Is(err, os.ErrNotExist) {
		if optional {
			return nil
		}
		return invalidServiceState("service definition is unsupported")
	}
	if err != nil {
		return invalidServiceState("service definition is unsupported")
	}
	return invalidServiceState("service definition is unsupported")
}

func (a *SystemdAdapter) DisableAutostartAndStop(ctx context.Context) error {
	def, err := a.InspectDefinition(ctx)
	if err != nil {
		return err
	}
	if def.Status == StatusNotInstalled {
		return nil
	}
	for _, link := range def.Links {
		// The unit mask is retained in the snapshot for uninstall/rollback, but
		// it is the stop barrier, not an autostart link to disable.
		if link.Path == a.paths.UnitFile && link.Target == a.paths.DevNull {
			continue
		}
		link := link
		err := applyAction(ctx, a.hook, DefinitionAction{
			Kind:       DefinitionActionDisable,
			Path:       link.Path,
			TargetRole: "definition",
			OldState:   "enabled",
			NewState:   "disabled",
		}, func(ctx context.Context) error {
			return a.files.Remove(ctx, link.Path)
		})
		if err != nil {
			return err
		}
	}
	err = applyAction(ctx, a.hook, DefinitionAction{
		Kind: DefinitionActionMask,
		Path: a.paths.UnitFile, Link: a.paths.DevNull,
		TargetRole: "definition",
		OldState:   "unmasked",
		NewState:   "masked",
	}, func(ctx context.Context) error {
		if strings.Contains(a.paths.UnitFile, "/run/") {
			return invalidServiceState("runtime mask is not allowed")
		}
		return a.files.Mask(ctx, a.paths.UnitFile, a.paths.DevNull)
	})
	if err != nil {
		return err
	}
	if err := a.reload(ctx, true); err != nil {
		return err
	}
	return applyAction(ctx, a.hook, DefinitionAction{
		Kind:       DefinitionActionStop,
		TargetRole: "definition",
		OldState:   "running",
		NewState:   "stopped",
	}, func(ctx context.Context) error {
		return a.stopUnit(ctx)
	})
}

func (a *SystemdAdapter) WaitOwnedTreeExit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if incompleteProcessIdentity(a.last.Process) {
		return invalidServiceState("service process identity is unknown")
	}
	group := a.last.Process.Group
	if group == "" {
		if a.last.Running {
			return invalidServiceState("service process tree is unknown")
		}
		return nil
	}
	if a.tree == nil {
		return invalidServiceState("service process tree is unknown")
	}
	if err := waitUntil(ctx, a.clock, stopWait, func(ctx context.Context) (bool, error) {
		return a.tree.Empty(ctx, group)
	}); err != nil {
		return err
	}
	empty, err := a.tree.Empty(ctx, group)
	if err != nil {
		return err
	}
	if empty {
		return nil
	}
	if err := a.tree.SignalGroup(ctx, group, "TERM"); err != nil {
		return err
	}
	if err := waitUntil(ctx, a.clock, termWait, func(ctx context.Context) (bool, error) {
		return a.tree.Empty(ctx, group)
	}); err != nil {
		return err
	}
	empty, err = a.tree.Empty(ctx, group)
	if err != nil {
		return err
	}
	if empty {
		return nil
	}
	if err := a.tree.SignalGroup(ctx, group, "KILL"); err != nil {
		return err
	}
	if err := waitUntil(ctx, a.clock, killWait, func(ctx context.Context) (bool, error) {
		return a.tree.Empty(ctx, group)
	}); err != nil {
		return err
	}
	empty, err = a.tree.Empty(ctx, group)
	if err != nil {
		return err
	}
	if !empty {
		return invalidServiceState("managed process tree did not exit")
	}
	return nil
}

func (a *SystemdAdapter) WriteDefinition(ctx context.Context, def Definition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if def.Masked {
		if err := applyAction(ctx, a.hook, DefinitionAction{
			Kind: DefinitionActionMask, Path: a.paths.UnitFile, Link: a.paths.DevNull,
			TargetRole: "definition", OldState: "unmasked", NewState: "masked",
		}, func(ctx context.Context) error { return a.files.Mask(ctx, a.paths.UnitFile, a.paths.DevNull) }); err != nil {
			return err
		}
	} else if err := a.writeFiles(ctx, def.Files); err != nil {
		return err
	}
	if def.Enabled {
		err := applyAction(ctx, a.hook, DefinitionAction{
			Kind: DefinitionActionEnable,
			Path: a.paths.WantsLink, Link: a.paths.UnitFile,
			TargetRole: "definition",
			OldState:   "disabled",
			NewState:   "enabled",
		}, func(ctx context.Context) error {
			return a.files.Mask(ctx, a.paths.WantsLink, a.paths.UnitFile)
		})
		if err != nil {
			return err
		}
	}
	if err := a.reload(ctx, false); err != nil {
		return err
	}
	a.last = cloneDefinition(def)
	return nil
}

func (a *SystemdAdapter) RestoreDefinition(ctx context.Context, def Definition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if def.Masked {
		err := applyAction(ctx, a.hook, DefinitionAction{
			Kind: DefinitionActionMask,
			Path: a.paths.UnitFile, Link: a.paths.DevNull,
			TargetRole: "definition",
			OldState:   "unmasked",
			NewState:   "masked",
		}, func(ctx context.Context) error {
			return a.files.Mask(ctx, a.paths.UnitFile, a.paths.DevNull)
		})
		if err != nil {
			return err
		}
	} else if err := a.writeFiles(ctx, def.Files); err != nil {
		return err
	}
	for _, link := range def.Links {
		link := link
		err := applyAction(ctx, a.hook, DefinitionAction{
			Kind: DefinitionActionEnable,
			Path: link.Path, Link: link.Target,
			TargetRole: "definition",
			OldState:   "disabled",
			NewState:   "enabled",
		}, func(ctx context.Context) error {
			return a.files.Mask(ctx, link.Path, link.Target)
		})
		if err != nil {
			return err
		}
	}
	if err := a.reload(ctx, false); err != nil {
		return err
	}
	a.last = cloneDefinition(def)
	return nil
}

func (a *SystemdAdapter) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return applyAction(ctx, a.hook, DefinitionAction{
		Kind:       DefinitionActionStart,
		TargetRole: "definition",
		OldState:   "stopped",
		NewState:   "running",
	}, func(ctx context.Context) error {
		result, err := runAbsolute(ctx, a.runner, []string{a.paths.Systemctl, "--system", "start", "--", serviceUnitName})
		if err != nil {
			return err
		}
		if err := requireZeroExit(result); err != nil {
			return err
		}
		def, err := a.Probe(ctx)
		if err != nil {
			return err
		}
		if !def.Running {
			return invalidServiceState("service status is unknown")
		}
		return nil
	})
}

func (a *SystemdAdapter) Probe(ctx context.Context) (Definition, error) {
	return a.InspectDefinition(ctx)
}

func (a *SystemdAdapter) writeFiles(ctx context.Context, files []DefinitionFile) error {
	for _, file := range files {
		file := file
		kind := DefinitionActionDefinition
		if file.Kind == "dropin" {
			kind = DefinitionActionDropin
		}
		err := applyAction(ctx, a.hook, DefinitionAction{
			Kind: kind,
			Path: file.Path, File: &file,
			TargetRole: "definition",
			OldState:   "absent",
			NewState:   "present",
		}, func(ctx context.Context) error {
			return a.files.Write(ctx, file)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *SystemdAdapter) showArgv() []string {
	return []string{
		a.paths.Systemctl, "--system", "show",
		"--property=LoadState",
		"--property=ActiveState",
		"--property=SubState",
		"--property=MainPID",
		"--property=ControlGroup",
		"--property=FragmentPath",
		"--property=DropInPaths",
		"--property=UnitFileState",
		"--",
		serviceUnitName,
	}
}

func (a *SystemdAdapter) reloadArgv() []string {
	return []string{a.paths.Systemctl, "--system", "daemon-reload"}
}

func (a *SystemdAdapter) reload(ctx context.Context, verifyMasked bool) error {
	return applyAction(ctx, a.hook, DefinitionAction{
		Kind:       DefinitionActionReload,
		TargetRole: "definition",
		OldState:   "stale",
		NewState:   "reloaded",
	}, func(ctx context.Context) error {
		result, err := runAbsolute(ctx, a.runner, a.reloadArgv())
		if err != nil {
			return err
		}
		if err := requireZeroExit(result); err != nil {
			return err
		}
		if !verifyMasked {
			return nil
		}
		show, err := runAbsolute(ctx, a.runner, a.showArgv())
		if err != nil {
			return err
		}
		if err := requireZeroExit(show); err != nil {
			return err
		}
		props, err := parseSystemdShow(show.Stdout)
		if err != nil {
			return err
		}
		if props["LoadState"] != "masked" {
			return invalidServiceState("service status is unknown")
		}
		target, err := a.files.ReadLink(ctx, a.paths.UnitFile)
		if err != nil || target != a.paths.DevNull {
			return invalidServiceState("service status is unknown")
		}
		return nil
	})
}

func (a *SystemdAdapter) stopUnit(ctx context.Context) error {
	result, err := runAbsolute(ctx, a.runner, []string{a.paths.Systemctl, "--system", "stop", "--", serviceUnitName})
	if err != nil {
		return err
	}
	show, err := runAbsolute(ctx, a.runner, a.showArgv())
	if err != nil {
		return err
	}
	if err := requireZeroExit(show); err != nil {
		return err
	}
	props, err := parseSystemdShow(show.Stdout)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		if props["LoadState"] == "not-found" {
			return nil
		}
		return invalidServiceState("service manager query failed")
	}
	if (props["ActiveState"] == "inactive" || props["ActiveState"] == "failed") && props["MainPID"] == "0" {
		return nil
	}
	return invalidServiceState("service status is unknown")
}

func allowedDropin(dir, name string) bool {
	if dir == "" || !unixAbs(name) || !strings.HasSuffix(name, ".conf") {
		return false
	}
	prefix := dir
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	rest := strings.TrimPrefix(name, prefix)
	return rest != "" && !strings.Contains(rest, "/") && !strings.Contains(rest, "..")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func waitUntil(ctx context.Context, clock Clock, limit time.Duration, ready func(context.Context) (bool, error)) error {
	if clock == nil {
		clock = realClock{}
	}
	deadline := clock.Now().Add(limit)
	for {
		ok, err := ready(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		now := clock.Now()
		if !now.Before(deadline) {
			return nil
		}
		sleep := pollWait
		if remaining := deadline.Sub(now); remaining < sleep {
			sleep = remaining
		}
		if sleep <= 0 {
			return nil
		}
		if err := clock.Sleep(ctx, sleep); err != nil {
			return err
		}
	}
}
