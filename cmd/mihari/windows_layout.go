//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/buildinfo"
	"github.com/mihari-proxy/mihari/internal/cli"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/credential"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/tui"
	"github.com/mihari-proxy/mihari/internal/update"
)

func executeProcess(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if !daemonInvocation(args) {
		clientCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		closeSignal, err := platform.WindowsClientExitSignal(clientCtx, cancel)
		if err != nil {
			// Registration is optional for normal CLI operation. An updater will
			// refuse an unregistered live instance and request that it be closed.
			_, _ = fmt.Fprintln(stderr, err)
		} else {
			defer func() {
				if err := closeSignal(); err != nil {
					_, _ = fmt.Fprintln(stderr, err)
				}
			}()
		}
		ctx = clientCtx
	}
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
		var runtimeJob string
		if elevate.IsElevated() {
			var generation [16]byte
			if _, err := rand.Read(generation[:]); err != nil {
				return err
			}
			job, err := platform.CreateWindowsRuntimeJob(ctx, hex.EncodeToString(generation[:]))
			if err != nil {
				return err
			}
			// The process owns the native handles until os.Exit. Closing this
			// kill-on-close Job here would terminate the daemon itself.
			runtimeJob = job.Name()
		}
		root, err := prepareLocalRoot()
		if err != nil {
			return err
		}
		return runDaemonWith(ctx, daemonRunDeps{
			UpdateRuntimeJob: runtimeJob,
			StartupCleanup: func(ctx context.Context) error {
				executable, err := os.Executable()
				if err != nil {
					return err
				}
				return app.CleanupApplicationBinary(ctx, executable)
			},
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
	maintenance := &app.WindowsUpdateMaintenance{Client: localClient, Service: serviceManager, ObserveTargets: selfUpdateCompletion.ObserveReplacement, ManualDaemonStopped: func() { _, _ = fmt.Fprintln(os.Stderr, "请先按原方式启动 mihari daemon") }}
	selfUpdater.AcquireMaintenance = maintenance.Acquire
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
				return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInternal, Message: "resolve Mihari executable path"}, executableError)
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
					return finishWindowsUpdate(os.Stdout)
				},
				StartupCleanup: func(ctx context.Context) error { return app.CleanupApplicationBinary(ctx, executable) },
				Input:          os.Stdin,
				Output:         os.Stdout,
				ErrorOutput:    os.Stderr,
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

func finishWindowsUpdate(out io.Writer) error {
	return relaunchWithLocalRootCleanup(func() error {
		_, err := fmt.Fprintln(out, "请重新输入 mihari")
		return err
	})
}
