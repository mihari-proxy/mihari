package tui

import (
	"context"
	"io"

	tea "charm.land/bubbletea/v2"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// Options contains the control client and terminal streams used by the TUI.
type Options struct {
	Client  *controlclient.Client
	Service systempage.ServiceController
	Input   io.Reader
	Output  io.Writer
}

// Run starts the full-screen Mihari terminal interface and blocks until it exits.
func Run(ctx context.Context, options Options) error {
	model := NewModel()
	var controlSession *session.Session
	if options.Client != nil {
		controlSession = session.New(options.Client, session.Options{})
		model = newModelWithClientContext(ctx, controlSession.Start(ctx), options.Client)
		defer controlSession.Close()
	}
	if options.Service != nil {
		model.SetServiceController(options.Service)
	}
	program := tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithInput(options.Input),
		tea.WithOutput(options.Output),
	)
	_, err := program.Run()
	return err
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
