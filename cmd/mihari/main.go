package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/LeeShunEE/mihari/internal/buildinfo"
	"github.com/LeeShunEE/mihari/internal/cli"
	controlclient "github.com/LeeShunEE/mihari/internal/control/client"
	"github.com/LeeShunEE/mihari/internal/control/credential"
	"github.com/LeeShunEE/mihari/internal/control/transport"
	"github.com/LeeShunEE/mihari/internal/daemon"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	endpoint := transport.DefaultEndpoint()
	token, setupError := credential.LoadOrCreate(transport.DefaultCredentialPath())
	dependencies := cli.Dependencies{
		StatusClient: controlclient.New(endpoint, token),
		SetupError:   setupError,
		RunDaemon: func(ctx context.Context) error {
			return daemon.Run(ctx, daemon.Options{
				Endpoint: endpoint,
				Token:    token,
				Version:  buildinfo.Version,
			})
		},
	}
	os.Exit(cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, dependencies))
}
