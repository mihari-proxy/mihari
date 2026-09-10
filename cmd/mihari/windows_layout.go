//go:build windows

package main

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/buildinfo"
	"github.com/mihari-proxy/mihari/internal/cli"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/credential"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/tui"
	"github.com/mihari-proxy/mihari/internal/update"
	"io"
	"os"
	"path/filepath"
)

func executeProcess(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var diagnosticStderr io.Writer
	if daemonInvocation(args) && !daemonJSONOutput(args) && !service.IsInteractive() {
		diagnosticStderr = stderr
	}
	code := cli.Execute(ctx, args, stdout, stderr, legacyDependencies(diagnosticStderr, daemonLoggingFailureStderr(args, stderr)))
	_ = closeCachedLocalRoot()
	return code
}

func legacyDependencies(diagnosticStderr, loggingFailureStderr io.Writer) cli.Dependencies {

	endpoint := transport.DefaultEndpoint()
	localClient := controlclient.New(endpoint, "")
	var serviceManager *service.Manager
	ready := make(chan struct{})
	runDaemonBody := func(ctx context.Context) error {
		root, err := prepareLocalRoot()
		if err != nil {
			return err
		}
		return runDaemonWith(ctx, daemonRunDeps{
			Paths:                root.Paths,
			PrivateFS:            root.FS,
			Token:                root.Token,
			Version:              buildinfo.Version,
			Endpoint:             endpoint,
			Ready:                ready,
			DiagnosticStderr:     diagnosticStderr,
			LoggingFailureStderr: loggingFailureStderr,
			ServiceStatus: func() (string, error) {
				if serviceManager == nil {
					return string(service.StatusUnknown), nil
				}
				kind, err := serviceManager.Status()
				return string(kind), err
			},
		})
	}
	// When SCM launches ImagePath `mihari.exe daemon`, the process is non-interactive.
	// Manager.Run registers with the service control manager; a plain daemon body never
	// calls StartServiceCtrlDispatcher and Windows fails the start with a 30s timeout.
	serviceManager = service.New(service.Options{Run: runDaemonBody, Ready: ready})
	runDaemon := func(ctx context.Context) error {
		if !service.IsInteractive() {
			return serviceManager.Run()
		}
		return runDaemonBody(ctx)
	}
	selfUpdateCompletion := app.NewSelfUpdateServiceCompletion(serviceManager, localClient)
	selfUpdater := update.SelfUpdater{ObserveTargets: selfUpdateCompletion.ObserveReplacement, AfterReplacePrepared: selfUpdateCompletion.AfterPreparedReplace}
	executable, executableError := os.Executable()
	var uninstaller *app.Uninstaller
	if paths, err := defaultAbsolutePaths(); err == nil {
		cwd, cwdErr := os.Getwd()
		if cwdErr == nil {
			defaults := platform.SystemLayoutDefaults()
			defaults.BaseDir = paths.Root
			defaults.TrustedHome = filepath.Dir(paths.Root)
			layout, layoutErr := platform.ResolveLayout(platform.LayoutInput{CWD: cwd, Data: paths.Root, InstallRoot: os.Getenv("MIHARI_INSTALL_ROOT")}, defaults)
			if layoutErr == nil {
				uninstaller = app.NewUninstaller(layout, app.UninstallerOptions{
					Service:  serviceManager,
					Elevated: elevate.IsElevated,
					ProbeDaemon: func(ctx context.Context) (bool, error) {
						token, err := credential.Load(layout.CredentialPath)
						if errors.Is(err, os.ErrNotExist) {
							return false, nil
						}
						if err != nil {
							return false, err
						}
						_, err = controlclient.New(layout.ControlEndpoint, token).Status(ctx)
						if err == nil {
							return true, nil
						}
						var apiErr protocol.APIError
						if errors.As(err, &apiErr) && apiErr.Code == protocol.CodeDaemonUnavailable {
							return false, nil
						}
						return false, err
					},
				})
			}
		}
	}
	runInstallValidation := func(ctx context.Context, transactionID string) error {
		return runNativeInstallValidation(ctx, transactionID, buildinfo.Version, nil)
	}

	dependencies := cli.Dependencies{
		StatusClient:           localClient,
		RuntimeClient:          localClient,
		SubscriptionClient:     localClient,
		PanelClient:            localClient,
		SystemProxyClient:      localClient,
		TunClient:              localClient,
		ServiceController:      serviceManager,
		Uninstaller:            uninstaller,
		CloseForPurgeUninstall: closeCachedLocalRoot,
		SelfUpdater:            selfUpdater,
		OpenBrowser:            platform.OpenBrowser,
		Interactive:            isInteractiveTerminal(os.Stdin, os.Stdout),
		PrepareLocalRoot:       prepareLocalRootForClient(localClient),
		RunTUI: func(ctx context.Context) error {
			if executableError != nil {
				return protocol.APIError{Code: protocol.CodeInternal, Message: "resolve Mihari executable path"}
			}
			root, err := prepareLocalRoot()
			if err != nil {
				return err
			}
			return tui.Run(ctx, tui.Options{
				Client:         localClient,
				Service:        serviceManager,
				Uninstaller:    uninstaller,
				SelfUpdater:    selfUpdater,
				CurrentVersion: buildinfo.Version,
				BinaryPath:     executable,
				Elevated:       elevate.IsElevated,
				Relaunch: func() error {
					return relaunchWithLocalRootCleanup(func() error {
						return platform.Relaunch(executable, tuiRelaunchArgs(executable), os.Environ())
					})
				},
				Input:       os.Stdin,
				Output:      os.Stdout,
				ErrorOutput: os.Stderr,
				OpenLogging: func(ctx context.Context) (tui.LoggingResources, error) {
					return openTUILogging(ctx, root.Paths, root.Token, root.FS, os.Stderr)
				},
				BuildExportLogs: buildExportLogs(root.Paths),
			})
		},
		RunDaemon:            runDaemon,
		RunInstallValidation: runInstallValidation,
	}
	return dependencies
}
