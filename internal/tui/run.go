package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	tea "charm.land/bubbletea/v2"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
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
}

// Run starts the full-screen Mihari terminal interface and blocks until it exits.
func Run(ctx context.Context, options Options) error {
	model := NewModel()
	var controlSession *session.Session
	var closeSessionOnce sync.Once
	closeSession := func() {
		closeSessionOnce.Do(func() {
			if controlSession != nil {
				controlSession.Close()
			}
		})
	}
	defer closeSession()
	if options.Client != nil {
		controlSession = session.New(options.Client, session.Options{})
		model = newModelWithClientContext(ctx, controlSession.Start(ctx), options.Client)
	}
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
	return finishRun(final, err, options.Output, options.Relaunch, closeSession)
}

func finishRun(final tea.Model, runErr error, warningWriter io.Writer, relaunch func() error, cleanup func()) error {
	if cleanup != nil {
		cleanup()
	}
	if runErr != nil {
		return runErr
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
		return nil
	}
	var warningErr error
	if warning != "" && warningWriter != nil {
		if _, err := fmt.Fprintf(warningWriter, "Warning: %s\n", warning); err != nil {
			warningErr = fmt.Errorf("write Mihari update warning: %w", err)
		}
	}
	if relaunch == nil {
		return errors.Join(warningErr, fmt.Errorf("relaunch updated Mihari: relaunch is unavailable"))
	}
	if err := relaunch(); err != nil {
		return errors.Join(warningErr, fmt.Errorf("relaunch updated Mihari: %w", err))
	}
	return nil
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
