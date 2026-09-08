//go:build linux || darwin

package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"runtime"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/buildinfo"
	"github.com/mihari-proxy/mihari/internal/cli"
	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/credential"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"github.com/mihari-proxy/mihari/internal/tui"
)

func executeProcess(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	pure := cli.Dependencies{RunSystemServiceDaemon: func(context.Context) error { return os.ErrInvalid }, RunInstallValidation: func(ctx context.Context, id string) error {
		return app.ClassifyUnixLocalError(runNativeInstallValidation(ctx, id, buildinfo.Version))
	}, ServiceApply: func(context.Context, app.InstallRequest) (app.InstallResult, error) {
		return app.InstallResult{}, os.ErrInvalid
	}}
	return executeWithAssembly(ctx, args, stdout, stderr, pure, func(ctx context.Context) (cli.Dependencies, error) {
		layout, uid, err := platform.CaptureLayout(ctx)
		if err != nil {
			return pure, app.ClassifyUnixLocalError(err)
		}
		binary, err := os.Executable()
		if err != nil {
			return pure, app.ClassifyUnixLocalError(err)
		}
		installer, err := app.NewUnixInstaller(layout, binary, buildinfo.Version)
		if err != nil {
			return pure, app.ClassifyUnixLocalError(err)
		}
		locator, err := layout.Locator(uid)
		if err != nil {
			return pure, app.ClassifyUnixLocalError(errors.Join(os.ErrInvalid, err))
		}
		client := controlclient.WithCredentialProvider(locator, credential.NewProvider(locator))
		deps := cli.Dependencies{StatusClient: client, RuntimeClient: client, SubscriptionClient: client, PanelClient: client, SystemProxyClient: client, TunClient: client, ServiceController: installer, ChannelQuery: installer.QueryChannel, ChannelSet: installer.SetChannel, ServiceApply: installer.Apply, ServiceAction: installer.RunService, SelfUpdater: installer, SelfUpdateChannel: installer.Channel, OpenBrowser: platform.OpenBrowser, Interactive: isInteractiveTerminal(os.Stdin, os.Stdout), RunInstallValidation: pure.RunInstallValidation}
		deps.RunTUI = func(ctx context.Context) error {
			opts := tui.Options{Client: client, Service: installer, SelfUpdater: installer, SelfUpdateChannel: installer.Channel, CurrentVersion: buildinfo.Version, BinaryPath: binary, Elevated: elevate.IsElevated, Input: os.Stdin, Output: stdout, ErrorOutput: stderr, Relaunch: func() error { return platform.Relaunch(binary, tuiRelaunchArgs(binary), os.Environ()) }, OpenLogging: func(ctx context.Context) (tui.LoggingResources, error) {
				fs, err := platform.OpenClientLogFS(ctx, layout)
				if err != nil {
					return tui.NewLoggingResources(nil, nil, nil), err
				}
				return openTUILogging(ctx, layout.ClientLogs, "", fs, stderr)
			}, BuildExportLogs: buildExportLogs(layout.ClientLogs)}
			if layout.Mode == platform.SystemMode {
				opts.BuildExportLogs = buildSystemExportLogs(layout.ClientLogs, client.OpenMachineSnapshot)
				opts.SplitLogging = true
				opts.UserLogDir = layout.ClientLogs.LogDir
				opts.UserExportDir = layout.ClientLogs.LogExportDir
			}
			return tui.Run(ctx, opts)
		}
		deps.RunDaemon = func(ctx context.Context) error {
			if layout.Mode == platform.SystemMode && uid != 0 {
				return protocol.APIError{Code: protocol.CodePermissionDenied, Message: "system daemon requires root"}
			}
			discover := func(ctx context.Context) (bool, error) {
				if layout.Mode == platform.PrivateMode {
					return false, nil
				}
				present, err := installer.InstalledSourcePresent(ctx)
				if err != nil {
					return false, err
				}
				if present {
					return true, nil
				}
				return platform.LegacyRootSourcePresent(ctx)
			}
			return app.RunUnixStartup(ctx, layout, discover, func(ctx context.Context, phase string) error {
				return runUnixDaemon(ctx, layout, uid, phase, installer)
			})
		}
		runSystemService := func(ctx context.Context, shareProcessGroup bool) error {
			return app.RunUnixSystemService(ctx, layout, func(ctx context.Context, phase string) error {
				return runUnixDaemonWithGate(ctx, layout, uid, phase, installer, app.InspectUnixServiceActivation, shareProcessGroup)
			})
		}
		deps.RunSystemServiceDaemon = func(ctx context.Context) error { return runSystemService(ctx, false) }
		deps.RunLaunchdServiceDaemon = launchdServiceDaemon(runSystemService)
		return deps, nil
	})
}

func runUnixDaemon(ctx context.Context, layout platform.ResolvedLayout, uid uint32, phase string, installer *app.UnixInstaller) error {
	return runUnixDaemonWithGate(ctx, layout, uid, phase, installer, app.InspectUnixActivation, false)
}

func runUnixDaemonWithGate(ctx context.Context, layout platform.ResolvedLayout, uid uint32, phase string, installer *app.UnixInstaller, inspect func(context.Context, platform.ResolvedLayout) (string, bool, error), shareProcessGroup bool) (resultErr error) {
	// Foreground/activation gate has completed before this first data-creating
	// capability. Security failures return directly, never through degraded mode.
	lease, err := platform.AcquireDaemonLease(ctx, layout)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lease.Close()) }()
	if uid == 0 {
		current, present, gateErr := inspect(ctx, layout)
		if gateErr != nil {
			return gateErr
		}
		if !present || current != phase {
			return protocol.APIError{Code: protocol.CodeInvalidState, Message: "install activation changed"}
		}
	}
	token, err := credential.LoadOrCreateOwned(ctx, layout, lease)
	if err != nil {
		return err
	}
	data, err := platform.OpenTrustedRoot(ctx, layout.Data.Root, platform.RootPolicy{Owner: uid, Mode: 0700})
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, data.Close()) }()
	var providers *subscription.ProviderStore
	if uid == 0 {
		providers, err = subscription.NewProviderStore(ctx, data)
		if err != nil {
			return err
		}
		if err = subscription.NewResourcePreparer(providers, nil, nil).Recover(ctx); err != nil {
			return err
		}
		provenance, err := core.NewProvenanceStore(ctx, data)
		if err != nil {
			return err
		}
		if err := core.RecoverProvenance(ctx, provenance); err != nil {
			return err
		}
	}
	logRoot, err := platform.OpenTrustedRoot(ctx, layout.Data.Root, platform.RootPolicy{Owner: uid, Mode: 0700})
	if err != nil {
		return err
	}
	fs, err := platform.NewPrivateFSFromRoot(logRoot)
	if err != nil {
		return errors.Join(err, logRoot.Close())
	}
	deps := daemonRunDeps{Paths: layout.Data, PrivateFS: fs, Token: token, Version: buildinfo.Version, Endpoint: layout.ControlEndpoint, ActivationPhase: phase, ShareProcessGroup: shareProcessGroup, MachineSnapshot: layout.Mode == platform.SystemMode, ServiceStatus: func() (string, error) { s, e := installer.Status(); return string(s), e }, Listen: func(ctx context.Context) (net.Listener, error) { return transport.ListenOwned(ctx, layout, lease) }}
	if uid == 0 {
		deps.PrepareRuntime = func(ctx context.Context, settings config.Settings) (app.RuntimeBuildOptions, error) {
			trusted, err := core.NewTrustedExecution(ctx, data, nil)
			if err != nil {
				return app.RuntimeBuildOptions{}, err
			}
			proxy := &url.URL{Scheme: "http", Host: settings.MixedAddr}
			downloader := subscription.NewDownloader(subscription.DownloaderOptions{ProxyURL: proxy})
			return app.RuntimeBuildOptions{TrustedCore: trusted, Resources: subscription.NewResourcePreparer(providers, downloader, downloader), RootConfigInput: func(_ context.Context, _ subscription.Document, settings config.Settings) (subscription.PolicyInput, error) {
				return subscription.PolicyInput{CoreTag: "v1.19.30", OS: runtime.GOOS, Arch: runtime.GOARCH, Settings: settings}, nil
			}}, nil
		}
	}
	return runDaemonWith(ctx, deps)
}
