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
	defer func() { resultErr = errors.Join(resultErr, listener.Close()) }()
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
	l.closeOnce.Do(func() { l.closeErr = l.Listener.Close() })
	return l.closeErr
}
