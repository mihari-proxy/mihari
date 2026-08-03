package tui

import (
	"context"
	"io"

	tea "charm.land/bubbletea/v2"
	controlclient "github.com/LeeShunEE/mihari/internal/control/client"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

// Options contains the control client and terminal streams used by the TUI.
type Options struct {
	Client *controlclient.Client
	Input  io.Reader
	Output io.Writer
}

// Run starts the full-screen Mihari terminal interface and blocks until it exits.
func Run(ctx context.Context, options Options) error {
	program := tea.NewProgram(
		NewModel(),
		tea.WithContext(ctx),
		tea.WithInput(options.Input),
		tea.WithOutput(options.Output),
	)
	_, err := program.Run()
	return err
}

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
