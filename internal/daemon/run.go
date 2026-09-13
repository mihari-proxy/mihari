package daemon

import (
	"context"
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
	Listen             func(context.Context) (net.Listener, error)
	OnReady            func() error
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
		return err
	}
	defer listener.Close()
	if options.OnReady != nil {
		if err := options.OnReady(); err != nil {
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
		go func() { runtimeDone <- options.Runtime.Run(ctx) }()
	}
	runtimeAPI, _ := options.Runtime.(controlserver.RuntimeAPI)
	server := controlserver.New(controlserver.Options{Token: options.Token, Store: store, Runtime: runtimeAPI, Onboarding: options.Onboarding, SnapshotSource: options.SnapshotSource, DiagnosticReporter: options.DiagnosticReporter})
	serverError := server.Serve(ctx, listener)
	cancel()
	if runtimeDone != nil {
		<-runtimeDone
	}
	return serverError
}
