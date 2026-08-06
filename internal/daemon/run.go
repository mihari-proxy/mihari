package daemon

import (
	"context"
	"time"

	controlserver "github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/state"
)

type Options struct {
	Endpoint string
	Token    string
	Version  string
	Ready    chan<- struct{}
	Store    *state.Store
	Runtime  Runtime
}

type Runtime interface {
	Run(context.Context) error
}

func Run(parent context.Context, options Options) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	listener, err := transport.Listen(options.Endpoint)
	if err != nil {
		return err
	}
	defer listener.Close()
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
	server := controlserver.New(controlserver.Options{Token: options.Token, Store: store, Runtime: runtimeAPI})
	serverError := server.Serve(ctx, listener)
	cancel()
	if runtimeDone != nil {
		<-runtimeDone
	}
	return serverError
}
