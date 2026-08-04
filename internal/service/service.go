// Package service installs and controls Mihari as a native OS service.
package service

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	kardservice "github.com/kardianos/service"
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
	opts Options
}

// New returns a Manager with defaults.
func New(opts Options) *Manager {
	if len(opts.Arguments) == 0 {
		opts.Arguments = []string{"daemon"}
	}
	if opts.NewController == nil {
		opts.NewController = newKardianosController
	}
	return &Manager{opts: opts}
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

// Install registers the OS service.
func (m *Manager) Install() error {
	c, err := m.controller(nil)
	if err != nil {
		return err
	}
	return mapServiceError(c.Install())
}

// Uninstall removes the OS service.
func (m *Manager) Uninstall() error {
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

// Status reports the service state.
func (m *Manager) Status() (StatusKind, error) {
	c, err := m.controller(nil)
	if err != nil {
		return StatusUnknown, err
	}
	st, err := c.Status()
	if err != nil {
		return StatusUnknown, mapServiceError(err)
	}
	return st, nil
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

func mapServiceError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "not installed"), strings.Contains(lower, "does not exist"), strings.Contains(lower, "not found"):
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihari service is not installed"}
	case strings.Contains(lower, "access is denied"), strings.Contains(lower, "permission"), strings.Contains(lower, "operation not permitted"):
		return protocol.APIError{Code: protocol.CodePermissionDenied, Message: "administrator privileges are required; re-run from an elevated shell"}
	default:
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: fmt.Sprintf("service operation failed: %s", msg)}
	}
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
	}
	prog := &program{run: run}
	svc, err := kardservice.New(prog, cfg)
	if err != nil {
		return nil, protocol.APIError{Code: protocol.CodeInternal, Message: "create service definition"}
	}
	return &kardianosController{svc: svc}, nil
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
