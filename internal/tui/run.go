package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	tea "charm.land/bubbletea/v2"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// Options contains the control client and terminal streams used by the TUI.
type Options struct {
	Client         *controlclient.Client
	Service        systempage.ServiceController
	SelfUpdater    systempage.SelfUpdater
	CurrentVersion string
	BinaryPath     string
	Elevated       func() bool
	Relaunch       func() error
	Input          io.Reader
	Output         io.Writer
	OpenLogging    LoggingFactory
	ErrorOutput    io.Writer
}

// LocalLoggingHealth reports whether the local TUI file logger is available.
type LocalLoggingHealth interface {
	Available() bool
}

// LoggingResources owns the TUI file logging runtime and the shared local data-root capability.
type LoggingResources struct {
	Runtime   *logging.Runtime
	Redactor  *logging.Redactor
	PrivateFS *platform.PrivateFS
	Health    LocalLoggingHealth

	closeOnce sync.Once
	closeErr  error
}

// Available reports whether the TUI file logging runtime opened successfully.
func (r *LoggingResources) Available() bool {
	return r != nil && r.Runtime != nil
}

// Close releases the runtime before the shared data-root capability. It is safe to call repeatedly.
func (r *LoggingResources) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		var errs []error
		if r.Runtime != nil {
			errs = append(errs, r.Runtime.Close())
		}
		if r.PrivateFS != nil {
			errs = append(errs, r.PrivateFS.Close())
		}
		r.closeErr = errors.Join(errs...)
	})
	return r.closeErr
}

// LoggingFactory opens TUI-local file logging using the Run context.
type LoggingFactory func(context.Context) (LoggingResources, error)

// Run starts the full-screen Mihari terminal interface and blocks until it exits.
func Run(ctx context.Context, options Options) error {
	resources := LoggingResources{}
	if options.OpenLogging != nil {
		opened, err := options.OpenLogging(ctx)
		resources = opened
		if err != nil {
			reportTUILoggingBootstrapFailure(options.ErrorOutput, resources.Redactor)
		}
	}
	health := resources.Health
	if health == nil {
		health = &resources
	}
	if resources.Runtime != nil && resources.Runtime.Logger() != nil {
		resources.Runtime.Logger().Info("tui started")
	}

	model := NewModel()
	var controlSession *session.Session
	if options.Client != nil {
		controlSession = session.New(options.Client, session.Options{})
		model = newModelWithClientContext(ctx, controlSession.Start(ctx), options.Client)
	}
	model.SetLoggingHealth(health)
	if options.Service != nil {
		model.SetServiceController(options.Service)
	}
	model.SetSelfUpdater(options.SelfUpdater, options.CurrentVersion, options.BinaryPath, options.Elevated)
	program := tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithInput(options.Input),
		tea.WithOutput(options.Output),
	)
	final, err := program.Run()
	var closeOnce sync.Once
	var closeErr error
	cleanup := func(tea.Model) error {
		closeOnce.Do(func() {
			if controlSession != nil {
				controlSession.Close()
			}
			closeErr = resources.Close()
			if closeErr != nil {
				reportTUILoggingCleanupFailure(options.ErrorOutput, resources.Redactor, closeErr)
			}
		})
		return closeErr
	}
	return finishRun(final, err, options.Output, options.Relaunch, cleanup)
}

func finishRun(final tea.Model, runErr error, warningWriter io.Writer, relaunch func() error, cleanup func(tea.Model) error) error {
	var cleanupErr error
	if cleanup != nil {
		cleanupErr = cleanup(final)
	}
	if runErr != nil {
		return errors.Join(runErr, cleanupErr)
	}
	var requested bool
	var warning string
	switch model := final.(type) {
	case Model:
		requested = model.RelaunchRequested()
		warning = model.RelaunchWarning()
	case *Model:
		requested = model.RelaunchRequested()
		warning = model.RelaunchWarning()
	}
	if !requested {
		return cleanupErr
	}
	var warningErr error
	if warning != "" && warningWriter != nil {
		if _, err := fmt.Fprintf(warningWriter, "Warning: %s\n", warning); err != nil {
			warningErr = fmt.Errorf("write Mihari update warning: %w", err)
		}
	}
	if relaunch == nil {
		return errors.Join(cleanupErr, warningErr, fmt.Errorf("relaunch updated Mihari: relaunch is unavailable"))
	}
	if err := relaunch(); err != nil {
		return errors.Join(cleanupErr, warningErr, fmt.Errorf("relaunch updated Mihari: %w", err))
	}
	return cleanupErr
}

func reportTUILoggingBootstrapFailure(out io.Writer, redactor *logging.Redactor) {
	if out == nil {
		return
	}
	message := "TUI file logging is unavailable"
	if redactor != nil {
		message = redactor.String(message)
	}
	_, _ = fmt.Fprintf(out, "Warning: %s\n", message)
}

func reportTUILoggingCleanupFailure(out io.Writer, redactor *logging.Redactor, err error) {
	if out == nil || err == nil {
		return
	}
	message := err.Error()
	if redactor != nil {
		message = redactor.String(message)
	}
	_, _ = fmt.Fprintf(out, "Warning: TUI file logging cleanup failed: %s\n", message)
}

// loadingModel is a minimal AltScreen sample used by run_test.go only.
// Production Run() constructs the full Root Shell via NewModel / newModelWithClientContext.
type loadingModel struct{}

func (loadingModel) Init() tea.Cmd { return nil }

func (model loadingModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "q", "ctrl+c":
			return model, tea.Quit
		}
	}
	return model, nil
}

func (loadingModel) View() tea.View {
	view := tea.NewView(ui.Connecting + "\n")
	view.AltScreen = true
	return view
}
