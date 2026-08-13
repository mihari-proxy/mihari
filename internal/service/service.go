// Package service installs and controls Mihari as a native OS service.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	kardservice "github.com/kardianos/service"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

const (
	serviceName        = "mihari"
	serviceDisplayName = "Mihari"
	serviceDescription = "Local mihomo supervisor and Web GUI gateway managed by Mihari."
)

// StatusKind is a redacted service state for CLI/JSON.
type StatusKind string

const (
	StatusRunning      StatusKind = "running"
	StatusStopped      StatusKind = "stopped"
	StatusUnknown      StatusKind = "unknown"
	StatusNotInstalled StatusKind = "not_installed"
)

// RunFunc is the daemon body; it blocks until ctx is cancelled.
type RunFunc func(ctx context.Context) error

// Controller performs service manager operations (tests inject fakes).
type Controller interface {
	Install() error
	Uninstall() error
	Start() error
	Stop() error
	Restart() error
	Status() (StatusKind, error)
	Run() error
}

// Options configures which binary the OS service should launch.
type Options struct {
	// Executable is the absolute path to mihari. Empty uses the current process executable.
	Executable string
	// Arguments defaults to ["daemon"].
	Arguments []string
	// Run is required only for Run().
	Run RunFunc
	// NewController builds a Controller; tests replace it.
	NewController func(run RunFunc, executable string, arguments []string) (Controller, error)
}

// Manager is the production entry for service control.
type Manager struct {
	opts        Options
	stageBinary func(string) (string, error)
}

// New returns a Manager with defaults.
func New(opts Options) *Manager {
	if len(opts.Arguments) == 0 {
		opts.Arguments = []string{"daemon"}
	}
	if opts.NewController == nil {
		opts.NewController = newKardianosController
	}
	return &Manager{opts: opts, stageBinary: platform.StageInstalledBinary}
}

func (m *Manager) controller(run RunFunc) (Controller, error) {
	exe := m.opts.Executable
	if exe == "" {
		path, err := os.Executable()
		if err != nil {
			return nil, protocol.APIError{Code: protocol.CodeInternal, Message: "resolve mihari executable path"}
		}
		exe = path
	}
	return m.opts.NewController(run, exe, m.opts.Arguments)
}

// stageServiceBinary copies the source executable into the platform install
// directory and returns that absolute path. The OS service ImagePath must never
// point at a download folder or developer workspace copy.
func (m *Manager) stageServiceBinary() (string, error) {
	src := m.opts.Executable
	if src == "" {
		var err error
		src, err = os.Executable()
		if err != nil {
			return "", protocol.APIError{Code: protocol.CodeInternal, Message: "resolve mihari executable path"}
		}
	}
	dest, err := m.stageBinary(src)
	if err != nil {
		return "", protocol.APIError{
			Code:    protocol.CodeDataFailure,
			Message: "copy mihari into install directory failed",
			Details: map[string]any{"error": err.Error()},
		}
	}
	return dest, nil
}

// UpdateInstalledBinary synchronizes the current executable into the machine
// install directory and restarts an existing OS service from that copy. It
// reports whether a service installation was found. A missing service is a
// successful no-op.
func (m *Manager) UpdateInstalledBinary() (bool, error) {
	status, err := m.Status()
	if err != nil {
		return false, protocol.APIError{
			Code:    protocol.CodeInvalidState,
			Message: "Mihari updated, but the installed service status could not be determined",
		}
	}
	if status == StatusNotInstalled {
		return false, nil
	}
	if err := m.Stop(); err != nil && !isIgnorableStopError(err) {
		return true, protocol.APIError{
			Code:    protocol.CodeInvalidState,
			Message: "Mihari updated, but the installed service could not be stopped",
		}
	}
	if _, err := m.stageServiceBinary(); err != nil {
		if restartErr := m.Start(); restartErr != nil {
			return true, protocol.APIError{
				Code:    protocol.CodeInvalidState,
				Message: "Mihari updated, but service synchronization failed and the previous service could not be restarted",
			}
		}
		return true, protocol.APIError{
			Code:    protocol.CodeDataFailure,
			Message: "Mihari updated, but the installed service binary could not be synchronized",
		}
	}
	if err := m.Start(); err != nil {
		return true, protocol.APIError{
			Code:    protocol.CodeInvalidState,
			Message: "Mihari updated and synchronized the installed service binary, but the service could not be restarted",
		}
	}
	return true, nil
}

// Install copies mihari into the machine install directory, then registers the
// OS service with that installed path (never the caller's download location).
func (m *Manager) Install() error {
	installed, err := m.stageServiceBinary()
	if err != nil {
		return err
	}
	newController := m.opts.NewController
	if newController == nil {
		newController = newKardianosController
	}
	args := m.opts.Arguments
	if len(args) == 0 {
		args = []string{"daemon"}
	}
	c, err := newController(nil, installed, args)
	if err != nil {
		return err
	}
	return mapServiceError(c.Install())
}

// Uninstall stops the service (if running), then removes the OS service registration.
// Stopping first avoids Windows SCM "marked for deletion" while the process is still alive.
func (m *Manager) Uninstall() error {
	if err := m.prepareForRemoval(); err != nil {
		return err
	}
	c, err := m.controller(nil)
	if err != nil {
		return err
	}
	return mapServiceError(c.Uninstall())
}

// Start starts the service.
func (m *Manager) Start() error {
	c, err := m.controller(nil)
	if err != nil {
		return err
	}
	return mapServiceError(c.Start())
}

// Stop stops the service.
func (m *Manager) Stop() error {
	c, err := m.controller(nil)
	if err != nil {
		return err
	}
	return mapServiceError(c.Stop())
}

// Restart restarts the service.
func (m *Manager) Restart() error {
	c, err := m.controller(nil)
	if err != nil {
		return err
	}
	return mapServiceError(c.Restart())
}

// Reinstall stops the service, removes registration, copies this binary into the
// install directory, re-registers ImagePath, and starts the service.
func (m *Manager) Reinstall() error {
	// Uninstall already stops first; ignore missing service so a fresh install still proceeds.
	if err := m.Uninstall(); err != nil && !isServiceNotInstalled(err) {
		return err
	}
	if err := m.Install(); err != nil {
		return err
	}
	return m.Start()
}

// prepareForRemoval stops the service before uninstall/reinstall. Missing or
// already-stopped services are treated as success so deletion can proceed.
func (m *Manager) prepareForRemoval() error {
	err := m.Stop()
	if err == nil || isIgnorableStopError(err) {
		return nil
	}
	return err
}

func isServiceNotInstalled(err error) bool {
	var apiError protocol.APIError
	if errors.As(err, &apiError) {
		return apiError.Code == protocol.CodeInvalidState && strings.Contains(strings.ToLower(apiError.Message), "not installed")
	}
	return isNotInstalledError(err)
}

func isIgnorableStopError(err error) bool {
	if err == nil || isServiceNotInstalled(err) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "not been started") ||
		strings.Contains(lower, "has not been started") ||
		strings.Contains(lower, "not started") ||
		strings.Contains(lower, "already stopped") ||
		strings.Contains(lower, "service is not started")
}

// Status reports the service state.
// When the service is not registered, it returns StatusNotInstalled with a nil error
// so callers (CLI status, TUI badge) can distinguish "not installed" without elevation.
// On Windows, Access is denied from the default service library falls back to a
// low-privilege SCM query so non-admin users still see running/stopped/not_installed.
func (m *Manager) Status() (StatusKind, error) {
	c, err := m.controller(nil)
	if err != nil {
		return StatusUnknown, err
	}
	st, err := c.Status()
	if err == nil {
		return st, nil
	}
	if isNotInstalledError(err) {
		return StatusNotInstalled, nil
	}
	if isAccessDeniedError(err) {
		if st, qerr := platformQueryStatus(serviceName); qerr == nil {
			return st, nil
		}
	}
	return StatusUnknown, mapServiceError(err)
}

// Run executes under the service manager (or foreground when interactive).
func (m *Manager) Run() error {
	if m.opts.Run == nil {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "service run function is required"}
	}
	c, err := m.controller(m.opts.Run)
	if err != nil {
		return err
	}
	return mapServiceError(c.Run())
}

// IsInteractive reports whether this process is running outside the OS service
// manager. When false (Windows service session), the daemon entrypoint must call
// Manager.Run so the process registers with SCM (StartServiceCtrlDispatcher).
func IsInteractive() bool {
	return kardservice.Interactive()
}

func mapServiceError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case isNotInstalledError(err):
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihari service is not installed"}
	case strings.Contains(lower, "access is denied"), strings.Contains(lower, "permission"), strings.Contains(lower, "operation not permitted"):
		return protocol.APIError{Code: protocol.CodePermissionDenied, Message: "administrator privileges are required; re-run from an elevated shell"}
	case strings.Contains(lower, "marked for deletion"), strings.Contains(lower, "1072"):
		return protocol.APIError{
			Code:    protocol.CodeInvalidState,
			Message: "service is marked for deletion; stop any remaining mihari process, close Services.msc, wait a few seconds or reboot, then retry",
		}
	case strings.Contains(lower, "did not respond to the start or control request"),
		strings.Contains(lower, "timely fashion"),
		strings.Contains(lower, "timeout was reached"),
		strings.Contains(lower, "1053"):
		// Classic SCM failure when the process never called StartServiceCtrlDispatcher
		// (e.g. old builds ran `daemon` as a plain CLI under the service ImagePath).
		return protocol.APIError{
			Code:    protocol.CodeInvalidState,
			Message: "service failed to start in time; ensure this mihari binary is used for the service ImagePath and reinstall/start after upgrading",
		}
	default:
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: fmt.Sprintf("service operation failed: %s", msg)}
	}
}

// isNotInstalledError reports OS/service-manager phrasing for a missing registration.
func isNotInstalledError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "not installed") ||
		strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "no such service") ||
		strings.Contains(lower, "the specified service does not exist")
}

type program struct {
	run    RunFunc
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *program) Start(s kardservice.Service) error {
	if p.run == nil {
		return nil
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		if err := p.run(p.ctx); err != nil {
			if logger, lerr := s.Logger(nil); lerr == nil {
				_ = logger.Error(err)
			}
		}
	}()
	return nil
}

func (p *program) Stop(kardservice.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		<-p.done
	}
	return nil
}

type kardianosController struct {
	svc kardservice.Service
}

func newKardianosController(run RunFunc, executable string, arguments []string) (Controller, error) {
	cfg := &kardservice.Config{
		Name:        serviceName,
		DisplayName: serviceDisplayName,
		Description: serviceDescription,
		Arguments:   arguments,
		Executable:  executable,
		// Pin the installer's resolved data root so LocalSystem/root does not
		// fall back to systemprofile or /root home and split state from the desktop client.
		EnvVars: installEnvVars(),
	}
	prog := &program{run: run}
	svc, err := kardservice.New(prog, cfg)
	if err != nil {
		return nil, protocol.APIError{Code: protocol.CodeInternal, Message: "create service definition"}
	}
	return &kardianosController{svc: svc}, nil
}

// installEnvVars returns environment variables written into the OS service unit
// at install time. MIHARI_DATA is always an absolute path.
func installEnvVars() map[string]string {
	root, err := platform.AbsoluteDataRoot()
	if err != nil || root == "" {
		root = platform.DefaultDataRoot()
	}
	return map[string]string{
		"MIHARI_DATA": root,
	}
}

func (c *kardianosController) Install() error   { return kardservice.Control(c.svc, "install") }
func (c *kardianosController) Uninstall() error { return kardservice.Control(c.svc, "uninstall") }
func (c *kardianosController) Start() error     { return kardservice.Control(c.svc, "start") }
func (c *kardianosController) Stop() error      { return kardservice.Control(c.svc, "stop") }
func (c *kardianosController) Restart() error   { return kardservice.Control(c.svc, "restart") }
func (c *kardianosController) Run() error       { return c.svc.Run() }

func (c *kardianosController) Status() (StatusKind, error) {
	st, err := c.svc.Status()
	if err != nil {
		if isNotInstalledError(err) {
			return StatusNotInstalled, nil
		}
		return StatusUnknown, err
	}
	switch st {
	case kardservice.StatusRunning:
		return StatusRunning, nil
	case kardservice.StatusStopped:
		return StatusStopped, nil
	default:
		return StatusUnknown, nil
	}
}
