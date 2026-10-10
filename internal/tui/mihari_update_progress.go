package tui

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func beginMihariUpdateProgress(out io.Writer, version string) func(string) error {
	var ticks <-chan time.Time
	var timer *time.Ticker
	if file, ok := out.(*os.File); ok {
		if info, err := file.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			timer = time.NewTicker(spinnerTickInterval)
			ticks = timer.C
		}
	}
	finish := mihariUpdateProgress(out, "Updating Mihari to "+diagnostics.EscapeTerminal(version), ticks)
	return func(result string) error {
		if timer != nil {
			timer.Stop()
		}
		return finish(result)
	}
}

// mihariUpdateProgress owns its animation worker until finish joins it. A nil
// tick channel emits ordinary lines for redirected output instead of animation.
func mihariUpdateProgress(out io.Writer, label string, ticks <-chan time.Time) func(string) error {
	if out == nil {
		out = io.Discard
	}
	write := func(text string) error {
		_, err := io.WriteString(out, text)
		return err
	}
	if ticks == nil {
		err := write(label + "\n")
		return func(result string) error {
			if err != nil {
				return fmt.Errorf("write Mihari update progress: %w", err)
			}
			if err := write(result + "\n"); err != nil {
				return fmt.Errorf("write Mihari update progress: %w", err)
			}
			return nil
		}
	}
	initialErr := write("\r" + ui.SpinnerLabel(time.Unix(0, 0), label))
	stop, joined := make(chan struct{}), make(chan error, 1)
	go func() {
		err := initialErr
		for {
			select {
			case <-stop:
				joined <- err
				return
			case at, open := <-ticks:
				if !open {
					ticks = nil
					continue
				}
				if err == nil {
					err = write("\r" + ui.SpinnerLabel(at, label))
				}
			}
		}
	}()
	return func(result string) error {
		close(stop)
		err := <-joined
		if err == nil {
			// Clear the old frame and any longer label before printing the outcome.
			err = write("\r\x1b[2K" + result + "\n")
		}
		if err != nil {
			return fmt.Errorf("write Mihari update progress: %w", err)
		}
		return nil
	}
}
