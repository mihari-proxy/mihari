package daemon

import (
	"context"
	"time"

	controlserver "github.com/LeeShunEE/mihari/internal/control/server"
	"github.com/LeeShunEE/mihari/internal/control/transport"
	"github.com/LeeShunEE/mihari/internal/state"
)

type Options struct {
	Endpoint string
	Token    string
	Version  string
	Ready    chan<- struct{}
}

func Run(ctx context.Context, options Options) error {
	listener, err := transport.Listen(options.Endpoint)
	if err != nil {
		return err
	}
	defer listener.Close()
	if options.Ready != nil {
		close(options.Ready)
	}

	store := state.NewStore(state.Snapshot{
		Revision:  0,
		Version:   options.Version,
		StartedAt: time.Now().UTC(),
		Health:    "ok",
	})
	server := controlserver.New(controlserver.Options{Token: options.Token, Store: store})
	return server.Serve(ctx, listener)
}
