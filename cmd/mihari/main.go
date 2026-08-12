package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/buildinfo"
	"github.com/mihari-proxy/mihari/internal/cli"
	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/credential"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/tui"
	"github.com/mihari-proxy/mihari/internal/update"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	endpoint := transport.DefaultEndpoint()
	token, setupError := credential.LoadOrCreate(transport.DefaultCredentialPath())
	localClient := controlclient.New(endpoint, token)
	runDaemonBody := func(ctx context.Context) error {
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
	}
	// When SCM launches ImagePath `mihari.exe daemon`, the process is non-interactive.
	// Manager.Run registers with the service control manager; a plain daemon body never
	// calls StartServiceCtrlDispatcher and Windows fails the start with a 30s timeout.
	serviceManager := service.New(service.Options{Run: runDaemonBody})
	runDaemon := func(ctx context.Context) error {
		if !service.IsInteractive() {
			return serviceManager.Run()
		}
		return runDaemonBody(ctx)
	}
	selfUpdater := update.SelfUpdater{
		AfterReplace: func(ctx context.Context, _ string) error {
			// Best-effort restart when service is installed; ignore not-installed.
			return restartServiceAfterReplace(serviceManager.Restart)
		},
	}
	executable, executableError := os.Executable()
	dependencies := cli.Dependencies{
		StatusClient:       localClient,
		RuntimeClient:      localClient,
		SubscriptionClient: localClient,
		PanelClient:        localClient,
		SystemProxyClient:  localClient,
		TunClient:          localClient,
		ServiceController:  serviceManager,
		SelfUpdater:        selfUpdater,
		OpenBrowser:        platform.OpenBrowser,
		SetupError:         setupError,
		Interactive:        isInteractiveTerminal(os.Stdin, os.Stdout),
		RunTUI: func(ctx context.Context) error {
			if executableError != nil {
				return protocol.APIError{Code: protocol.CodeInternal, Message: "resolve Mihari executable path"}
			}
			return tui.Run(ctx, tui.Options{
				Client:         localClient,
				Service:        serviceManager,
				SelfUpdater:    selfUpdater,
				CurrentVersion: buildinfo.Version,
				BinaryPath:     executable,
				Elevated:       elevate.IsElevated,
				Relaunch: func() error {
					return platform.Relaunch(executable, tuiRelaunchArgs(executable), os.Environ())
				},
				Input:  os.Stdin,
				Output: os.Stdout,
			})
		},
		RunDaemon: runDaemon,
	}
	os.Exit(cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, dependencies))
}

func restartServiceAfterReplace(restart func() error) error {
	err := restart()
	if err == nil {
		return nil
	}
	var api protocol.APIError
	if errors.As(err, &api) && api.Code == protocol.CodeInvalidState && strings.Contains(strings.ToLower(api.Message), "not installed") {
		return nil
	}
	return err
}

func tuiRelaunchArgs(binary string) []string {
	return []string{binary}
}

func isInteractiveTerminal(input, output *os.File) bool {
	inputInfo, err := input.Stat()
	if err != nil || inputInfo.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	outputInfo, err := output.Stat()
	return err == nil && outputInfo.Mode()&os.ModeCharDevice != 0
}
