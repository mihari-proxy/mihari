package tui

import (
	"context"
	"errors"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"
)

func finishCompleteUninstallRun(ctx context.Context, final tea.Model, runErr error, out io.Writer, cleanup func(tea.Model) error, uninstaller Uninstaller) error {
	var cleanupErr error
	if cleanup != nil {
		cleanupErr = cleanup(final)
	}
	if runErr != nil {
		return errors.Join(runErr, cleanupErr)
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	if uninstaller == nil {
		return errors.New("complete uninstall is unavailable")
	}
	return uninstaller.RunForce(ctx, func(message string) {
		if out != nil {
			_, _ = fmt.Fprintln(out, message)
		}
	})
}
