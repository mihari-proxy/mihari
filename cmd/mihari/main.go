package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/LeeShunEE/mihari/internal/app"
	"github.com/LeeShunEE/mihari/internal/buildinfo"
	"github.com/LeeShunEE/mihari/internal/cli"
	"github.com/LeeShunEE/mihari/internal/config"
	controlclient "github.com/LeeShunEE/mihari/internal/control/client"
	"github.com/LeeShunEE/mihari/internal/control/credential"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/control/transport"
	"github.com/LeeShunEE/mihari/internal/daemon"
	"github.com/LeeShunEE/mihari/internal/platform"
	"github.com/LeeShunEE/mihari/internal/tui"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	endpoint := transport.DefaultEndpoint()
	token, setupError := credential.LoadOrCreate(transport.DefaultCredentialPath())
	localClient := controlclient.New(endpoint, token)
	dependencies := cli.Dependencies{
		StatusClient:       localClient,
		RuntimeClient:      localClient,
		SubscriptionClient: localClient,
		SetupError:         setupError,
		Interactive:        isInteractiveTerminal(os.Stdin, os.Stdout),
		RunTUI: func(ctx context.Context) error {
			return tui.Run(ctx, tui.Options{
				Client: localClient,
				Input:  os.Stdin,
				Output: os.Stdout,
			})
		},
		RunDaemon: func(ctx context.Context) error {
			paths := platform.DefaultPaths()
			if err := paths.EnsureDirs(); err != nil {
				return protocol.APIError{Code: protocol.CodeDataFailure, Message: "create mihari data directories"}
			}
			settings, created, err := config.LoadOrCreateResult(paths.Settings)
			if err != nil {
				return err
			}
			assembly, err := app.BuildRuntimeWithOptions(paths, settings, buildinfo.Version, os.Stdout, os.Stderr, app.RuntimeBuildOptions{
				InitialSetupRequired: created, SettingsPath: paths.Settings,
			})
			if err != nil {
				return err
			}
			return daemon.Run(ctx, daemon.Options{
				Endpoint: endpoint,
				Token:    token,
				Version:  buildinfo.Version,
				Store:    assembly.Store,
				Runtime:  assembly.Manager,
			})
		},
	}
	os.Exit(cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, dependencies))
}

func isInteractiveTerminal(input, output *os.File) bool {
	inputInfo, err := input.Stat()
	if err != nil || inputInfo.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	outputInfo, err := output.Stat()
	return err == nil && outputInfo.Mode()&os.ModeCharDevice != 0
}
