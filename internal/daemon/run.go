package daemon

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	controlserver "github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/state"
)

type Options struct {
	Onboarding         controlserver.OnboardingAPI
	SnapshotSource     logging.MachineSnapshotSource
	DiagnosticReporter diagnostics.Reporter
	DiagnosticHistory  *diagnostics.History
	Listen             func(context.Context) (net.Listener, error)
	OnReady            func() error
	StartupCleanup     func(context.Context) error
	Endpoint           string
	Token              string
	Version            string
	Ready              chan<- struct{}
	Store              *state.Store
	Runtime            Runtime
	ValidationMode     bool
}

type Runtime interface {
	Run(context.Context) error
}

// Run owns the local control listener and joins the optional runtime when serving ends.
func Run(parent context.Context, options Options) error {
	if options.ValidationMode && (options.Listen == nil || options.OnReady == nil) {
		return activationRefused()
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	listen := options.Listen
	if listen == nil {
		listen = func(context.Context) (net.Listener, error) { return transport.Listen(options.Endpoint) }
	}
	listener, err := listen(ctx)
	if err != nil {
		reportFailure(ctx, options.DiagnosticReporter, "listener.open.failed", err)
		return err
	}
	defer func() {
		if err := listener.Close(); err != nil && !onlyListenerClosed(err) {
			reportCleanup(ctx, options.DiagnosticReporter, "listener.cleanup.failed", err)
		}
	}()
	if !options.ValidationMode && options.StartupCleanup != nil {
		reportCleanup(ctx, options.DiagnosticReporter, "startup.cleanup.failed", options.StartupCleanup(ctx))
	}
	if options.OnReady != nil {
		if err := options.OnReady(); err != nil {
			reportFailure(ctx, options.DiagnosticReporter, "ready.failed", err)
			return err
		}
	}
	if options.Ready != nil {
		close(options.Ready)
	}

	store := options.Store
	if store == nil {
		store = state.NewStore(state.Snapshot{
			Revision:  0,
			Version:   options.Version,
			StartedAt: time.Now().UTC(),
			Health:    "ok",
		})
	}
	var runtimeDone chan error
	if options.Runtime != nil {
		runtimeDone = make(chan error, 1)
		go func() {
			err := options.Runtime.Run(ctx)
			reportFailure(ctx, options.DiagnosticReporter, "runtime.failed", err)
			runtimeDone <- err
		}()
	}
	runtimeAPI, _ := options.Runtime.(controlserver.RuntimeAPI)
	server := controlserver.New(controlserver.Options{Token: options.Token, Store: store, Runtime: runtimeAPI, Onboarding: options.Onboarding, SnapshotSource: options.SnapshotSource, DiagnosticReporter: options.DiagnosticReporter, DiagnosticHistory: options.DiagnosticHistory})
	serverError := server.Serve(ctx, listener)
	cancel()
	if runtimeDone != nil {
		<-runtimeDone
	}
	return serverError
}

func reportFailure(ctx context.Context, reporter diagnostics.Reporter, event string, err error) {
	if err == nil || reporter == nil || diagnostics.AlreadyReported(err) {
		return
	}
	if level, emit := diagnostics.FailureLevel(ctx, err); emit {
		reporter(ctx, diagnostics.Record{Component: "daemon", Event: event, Level: level, Err: err})
	}
}

func reportCleanup(ctx context.Context, reporter diagnostics.Reporter, event string, err error) {
	if err == nil || reporter == nil || diagnostics.AlreadyReported(err) {
		return
	}
	if level, emit := diagnostics.FailureLevel(ctx, err); emit {
		if level > slog.LevelWarn {
			level = slog.LevelWarn
		}
		reporter(ctx, diagnostics.Record{Component: "daemon", Event: event, Level: level, Err: err})
	}
}

func onlyListenerClosed(err error) bool {
	if err == nil {
		return true
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		children := wrapped.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !onlyListenerClosed(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		if child := wrapped.Unwrap(); child != nil {
			return onlyListenerClosed(child)
		}
	}
	return errors.Is(err, net.ErrClosed)
}
