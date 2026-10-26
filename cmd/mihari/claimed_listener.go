package main

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/mihari-proxy/mihari/internal/control/transport"
)

// runDaemonWithClaimedListener reserves the Windows control endpoint before
// business files are opened or changed, keeping duplicate startups out of assembly.
func runDaemonWithClaimedListener(ctx context.Context, deps daemonRunDeps) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	listen := deps.Listen
	if listen == nil {
		listen = func(context.Context) (net.Listener, error) { return transport.Listen(deps.Endpoint) }
	}
	raw, err := listen(ctx)
	if err != nil {
		reportDaemonStartupFailure(deps.DiagnosticStderr, "claim control listener", err)
		return err
	}
	listener := &claimedDaemonListener{Listener: raw}
	defer func() {
		if closeErr, closedHere := listener.closeOwned(); closedHere {
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	deps.Listen = func(context.Context) (net.Listener, error) { return listener, nil }
	return runDaemonWith(ctx, deps)
}

type claimedDaemonListener struct {
	net.Listener
	closeOnce sync.Once
	closeErr  error
}

func (l *claimedDaemonListener) Close() error {
	err, _ := l.closeOwned()
	return err
}

// closeOwned reports whether this caller performed the physical close. Once the
// daemon closes its listener, it also owns reporting that cleanup error; the
// assembly guard must not turn it into a second failure on a successful exit.
func (l *claimedDaemonListener) closeOwned() (err error, closedHere bool) {
	l.closeOnce.Do(func() {
		closedHere = true
		l.closeErr = l.Listener.Close()
	})
	return l.closeErr, closedHere
}
