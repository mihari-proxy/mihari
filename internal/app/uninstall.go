package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
)

const uninstallStopTimeout = 30 * time.Second

// UninstallService performs the existing operating-system service actions.
type UninstallService interface {
	Uninstall() error
	Status() (service.StatusKind, error)
}

// UninstallerOptions supplies the narrow dependencies used by Uninstaller.
type UninstallerOptions struct {
	Service      UninstallService
	Uninstall    func(context.Context) error
	Status       func(context.Context) (service.StatusKind, error)
	Elevated     func() bool
	ProbeDaemon  func(context.Context) (bool, error)
	Remove       func(string) error
	PollInterval time.Duration
}

// Uninstaller removes an existing Mihari service and its recognized files.
type Uninstaller struct {
	layout         platform.ResolvedLayout
	opts           UninstallerOptions
	controlRoot    string
	controlRootErr error
}

// NewUninstaller captures the resolved layout and uninstall dependencies.
func NewUninstaller(layout platform.ResolvedLayout, opts UninstallerOptions) *Uninstaller {
	return newUninstaller(layout, opts, platform.UninstallControlRoot)
}

type uninstallControlRootResolver func() (string, error)

func newUninstaller(layout platform.ResolvedLayout, opts UninstallerOptions, resolveControlRoot uninstallControlRootResolver) *Uninstaller {
	if opts.Remove == nil {
		opts.Remove = os.RemoveAll
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 100 * time.Millisecond
	}
	var controlRoot string
	var controlRootErr error
	if resolveControlRoot == nil {
		controlRootErr = errors.New("Mihari installation control directory resolver is unavailable")
	} else {
		controlRoot, controlRootErr = resolveControlRoot()
	}
	return &Uninstaller{layout: layout, opts: opts, controlRoot: controlRoot, controlRootErr: controlRootErr}
}

// Preview verifies and returns the fixed roots selected for uninstall.
func (u *Uninstaller) Preview(ctx context.Context) ([]UninstallTarget, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if u.controlRootErr != nil {
		return nil, fmt.Errorf("resolve Mihari installation control directory: %w", u.controlRootErr)
	}
	targets := uninstallTargets(u.layout, u.controlRoot)
	if err := CheckUninstallFiles(ctx, targets); err != nil {
		return nil, err
	}
	return targets, nil
}

// Run stops and removes the service, then removes only roots verified by Preview.
func (u *Uninstaller) Run(ctx context.Context, progress func(string)) error {
	targets, err := u.Preview(ctx)
	if err != nil {
		return err
	}
	if u.opts.Elevated == nil || !u.opts.Elevated() {
		return errors.New("administrator privileges are required; re-run this command from an elevated shell")
	}

	reportUninstallProgress(progress, "Uninstalling Mihari service")
	if err := u.uninstallService(ctx); err != nil {
		return fmt.Errorf("uninstall Mihari service: %w", err)
	}
	if err := u.waitForDaemonStop(ctx, progress); err != nil {
		return err
	}
	if err := CheckUninstallFiles(ctx, targets); err != nil {
		return err
	}

	for phase := 0; phase < 3; phase++ {
		for _, target := range targets {
			if uninstallTargetPhase(target) != phase {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := removeUninstallTarget(target, u.opts.Remove); err != nil {
				if target.Kind == "program" && runtime.GOOS == "windows" {
					return fmt.Errorf("remove %s: %w. Run Mihari from a separate copy and retry the uninstall.", target.Path, err)
				}
				return fmt.Errorf("remove %s: %w", target.Path, err)
			}
			reportUninstallProgress(progress, "Removed "+target.Path)
		}
	}
	reportUninstallProgress(progress, "Mihari has been completely uninstalled")
	return nil
}

func (u *Uninstaller) uninstallService(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	status, err := u.serviceStatus(ctx)
	if err != nil {
		return fmt.Errorf("check Mihari service status: %w", err)
	}
	switch status {
	case service.StatusNotInstalled:
		return nil
	case service.StatusUnknown:
		return errors.New("cannot confirm that the Mihari service is installed")
	}
	if u.opts.Uninstall != nil {
		return u.opts.Uninstall(ctx)
	}
	if u.opts.Service == nil {
		return errors.New("Mihari service uninstall is unavailable")
	}
	return u.opts.Service.Uninstall()
}

func (u *Uninstaller) serviceStatus(ctx context.Context) (service.StatusKind, error) {
	if err := ctx.Err(); err != nil {
		return service.StatusUnknown, err
	}
	if u.opts.Status != nil {
		return u.opts.Status(ctx)
	}
	if u.opts.Service == nil {
		return service.StatusUnknown, errors.New("Mihari service status is unavailable")
	}
	return u.opts.Service.Status()
}

func (u *Uninstaller) waitForDaemonStop(ctx context.Context, progress func(string)) error {
	if u.opts.ProbeDaemon == nil {
		return errors.New("Mihari daemon activity probe is unavailable")
	}
	waitCtx, cancel := context.WithTimeout(ctx, uninstallStopTimeout)
	defer cancel()
	reported := false
	for {
		status, err := u.serviceStatus(waitCtx)
		if err != nil {
			return fmt.Errorf("check Mihari service status: %w", err)
		}
		if status == service.StatusUnknown {
			return errors.New("cannot confirm that the Mihari service has stopped")
		}
		active, err := u.opts.ProbeDaemon(waitCtx)
		if err != nil {
			return fmt.Errorf("check Mihari daemon activity: %w", err)
		}
		if (status == service.StatusStopped || status == service.StatusNotInstalled) && !active {
			return nil
		}
		if !reported {
			reportUninstallProgress(progress, "Waiting for Mihari to stop")
			reported = true
		}
		timer := time.NewTimer(u.opts.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-waitCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return errors.New("Mihari did not stop within 30 seconds; stop it and retry the uninstall")
		case <-timer.C:
		}
	}
}

func uninstallTargets(layout platform.ResolvedLayout, controlRoot string) []UninstallTarget {
	targets := make([]UninstallTarget, 0, 5)
	appendTarget := func(path, kind string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		for _, target := range targets {
			if sameUninstallPath(target.Path, path) {
				return
			}
		}
		targets = append(targets, UninstallTarget{Path: path, Kind: kind})
	}

	appendTarget(layout.Data.Root, "data")
	if layout.Mode == platform.SystemMode {
		appendTarget(layout.ClientLogs.Root, "logs")
	}
	appendTarget(layout.InstallRoot, "program")
	if layout.Mode == platform.SystemMode {
		appendTarget(layout.BaseDir, "base")
	}
	appendTarget(controlRoot, "control")
	return targets
}

func sameUninstallPath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func uninstallTargetPhase(target UninstallTarget) int {
	switch target.Kind {
	case "data", "logs":
		return 0
	case "program":
		return 1
	default:
		return 2
	}
}

func removeUninstallTarget(target UninstallTarget, remove func(string) error) error {
	info, err := os.Lstat(target.Path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return newUninstallFileError(target, ".", "symbolic link")
	}
	if !info.IsDir() {
		return newUninstallFileError(target, ".", "non-directory root")
	}
	return remove(target.Path)
}

func reportUninstallProgress(progress func(string), message string) {
	if progress != nil {
		progress(message)
	}
}
