package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type coreProgressClient interface {
	OperationStatus(context.Context, string) (protocol.OperationStatus, error)
}

const coreProgressInterval = 200 * time.Millisecond

// trackCoreInstallProgress redraws one stderr line until stop. JSON output stays a single envelope.
func trackCoreInstallProgress(ctx context.Context, json bool, client any, id string, errOut io.Writer) func() {
	if json || errOut == nil {
		return func() {}
	}
	watcher, ok := client.(coreProgressClient)
	if !ok {
		return func() {}
	}
	watchCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		followCoreProgress(watchCtx, watcher, id, errOut, coreProgressInterval, time.Now())
	}()
	return func() {
		cancel()
		<-done
	}
}

func followCoreProgress(ctx context.Context, client coreProgressClient, id string, errOut io.Writer, interval time.Duration, started time.Time) {
	if interval <= 0 {
		interval = coreProgressInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var previous string
	wrote := false
	for {
		previous, wrote = writeCoreProgress(ctx, client, id, errOut, started, previous, wrote)
		select {
		case <-ctx.Done():
			if wrote {
				_, _ = fmt.Fprintln(errOut)
			}
			return
		case <-ticker.C:
		}
	}
}

func writeCoreProgress(ctx context.Context, client coreProgressClient, id string, errOut io.Writer, started time.Time, previous string, wrote bool) (string, bool) {
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var progress *protocol.OperationProgress
	if status, err := controlclient.ObserveOperationProgress(queryCtx, client, id); err == nil {
		progress = status.Progress
	} else if ctx.Err() != nil {
		return previous, wrote
	}
	elapsed := time.Duration(0)
	if !started.IsZero() {
		elapsed = time.Since(started)
	}
	line := protocol.FormatCoreInstallProgress(progress, elapsed)
	if line == previous {
		return previous, wrote
	}
	padding := ""
	if len(previous) > len(line) {
		padding = strings.Repeat(" ", len(previous)-len(line))
	}
	if _, err := fmt.Fprintf(errOut, "\r%s%s", line, padding); err != nil {
		return previous, wrote
	}
	return line, true
}
