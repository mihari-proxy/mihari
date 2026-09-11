//go:build unix_security && (linux || darwin)

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
)

type securityNativeManager struct {
	files                     service.DefinitionStore
	paths                     service.SystemdPaths
	launchd                   service.LaunchdPaths
	running, loaded, disabled bool
	verbs                     []string
	dropins                   []string
	stopAuthority             *service.Definition
	stopBoot                  string
}

type securityNativeTree struct{ nativeBoundaryTree }

func (securityNativeTree) BootIdentity(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return installBootIdentity()
}

func (tree securityNativeTree) Identify(ctx context.Context, pid int) (service.ProcessIdentity, error) {
	boot, err := tree.BootIdentity(ctx)
	if err != nil {
		return service.ProcessIdentity{}, err
	}
	return securityNativeProcessIdentity(boot, pid, 100, 0), nil
}

func (m *securityNativeManager) Run(ctx context.Context, argv []string) (service.CommandResult, error) {
	if runtime.GOOS == "linux" {
		if len(argv) < 3 {
			return service.CommandResult{}, errors.New("unexpected systemd fixture command")
		}
		m.verbs = append(m.verbs, argv[2])
		switch argv[2] {
		case "start":
			m.running = true
		case "stop":
			m.running = false
		case "show":
			load, fragment, enabled := "loaded", m.paths.UnitFile, "disabled"
			if _, err := m.files.Read(ctx, m.paths.UnitFile); errors.Is(err, os.ErrNotExist) {
				load, fragment = "not-found", ""
			} else if err != nil {
				return service.CommandResult{}, err
			}
			if link, err := m.files.ReadLink(ctx, m.paths.UnitFile); err == nil && link == "/dev/null" {
				load, fragment, enabled = "masked", "/dev/null", "masked"
			}
			if enabled != "masked" {
				if _, err := m.files.ReadLink(ctx, m.paths.WantsLink); err == nil {
					enabled = "enabled"
				}
			}
			active, pid, group := "inactive", 0, ""
			if m.running {
				active, pid, group = "active", 123, "/system.slice/mihari.service"
			}
			return service.CommandResult{Stdout: []byte(fmt.Sprintf("LoadState=%s\nActiveState=%s\nSubState=dead\nMainPID=%d\nControlGroup=%s\nFragmentPath=%s\nDropInPaths=%s\nUnitFileState=%s\n", load, active, pid, group, fragment, strings.Join(m.existingDropins(ctx), " "), enabled))}, nil
		}
		return service.CommandResult{}, nil
	}
	if len(argv) < 2 {
		return service.CommandResult{}, errors.New("unexpected launchd fixture command")
	}
	m.verbs = append(m.verbs, argv[1])
	switch argv[1] {
	case "print":
		if !m.loaded {
			return service.CommandResult{ExitCode: 1, Stderr: []byte("Could not find service \"system/mihari\".\n")}, nil
		}
		state, pid := "active", 0
		if m.running {
			state, pid = "running", 123
		}
		return service.CommandResult{Stdout: []byte(fmt.Sprintf("system/mihari = {\n state = %s\n pid = %d\n}\n", state, pid))}, nil
	case "print-disabled":
		return service.CommandResult{Stdout: []byte(fmt.Sprintf("{\n \"mihari\" => %t\n}\n", m.disabled))}, nil
	case "disable":
		m.disabled = true
	case "enable":
		m.disabled = false
	case "bootout":
		m.loaded = false
		m.running = false
	case "bootstrap":
		m.loaded = true
		m.running = true
	default:
		return service.CommandResult{}, errors.New("unexpected launchd fixture verb")
	}
	return service.CommandResult{}, nil
}
func (m *securityNativeManager) adapter(hook service.ActionHook) service.RecoveryAdapter {
	if runtime.GOOS == "darwin" {
		adapter := service.NewSecurityLaunchdAdapter(m, m.files, securityNativeTree{}, hook, m.launchd)
		if m.stopAuthority != nil {
			adapter.BindStopAuthority(*m.stopAuthority, m.stopBoot)
		}
		return adapter
	}
	return service.NewSystemdAdapterWithConfig(service.SystemdConfig{Runner: m, Files: m.files, Paths: m.paths, Tree: nativeBoundaryTree{}, Hook: hook})
}
func securityFixtureForLayout(t *testing.T, layout platform.ResolvedLayout) *securityNativeInstall {
	t.Helper()
	unitDir := filepath.Join(filepath.Dir(layout.InstallRoot), "u")
	if err := os.MkdirAll(filepath.Join(unitDir, "w"), 0700); err != nil {
		t.Fatal(err)
	}
	paths := service.DefaultSystemdPaths()
	paths.UnitDir = unitDir
	paths.UnitFile = filepath.Join(unitDir, "mihari.service")
	paths.DropinDir = filepath.Join(unitDir, "mihari.service.d")
	paths.WantsLink = filepath.Join(unitDir, "w", "mihari.service")
	launch := service.DefaultLaunchdPaths()
	launch.Plist = filepath.Join(unitDir, "m.plist")
	manager := &securityNativeManager{files: service.NewUnixDefinitionStore(), paths: paths, launchd: launch}
	def, err := service.BuildUnixDefinition(layout, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		def.Files[0].Path = launch.Plist
	} else {
		def.Files[0].Path = paths.UnitFile
		def.Links = []service.DefinitionLink{{Path: paths.WantsLink, Target: paths.UnitFile, Owner: 0, Mode: 0777}}
	}
	return &securityNativeInstall{layout: layout, manager: manager, definition: def}
}

func (m *securityNativeManager) existingDropins(ctx context.Context) []string {
	var paths []string
	for _, path := range m.dropins {
		if _, err := m.files.Read(ctx, path); err == nil {
			paths = append(paths, path)
		}
	}
	return paths
}
