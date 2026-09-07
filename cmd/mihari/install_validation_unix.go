//go:build linux || darwin

package main

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/credential"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"net"
	"runtime"
)

func runNativeInstallValidation(ctx context.Context, id, version string) error {
	return app.RunInheritedValidation(ctx, id, func(ctx context.Context, layout platform.ResolvedLayout, locks *platform.OwnedDaemonLease, ready func(bool) error) (resultErr error) {
		data, err := platform.OpenTrustedRoot(ctx, layout.Data.Root, platform.RootPolicy{Owner: 0, Mode: 0700})
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, data.Close()) }()
		provenance, err := core.NewProvenanceStore(ctx, data)
		if err != nil {
			return err
		}
		providers, err := subscription.NewProviderStore(ctx, data)
		if err != nil {
			return err
		}
		// Logging consumes a separate capability; provenance/resources retain data.
		logRoot, err := platform.OpenTrustedRoot(ctx, layout.Data.Root, platform.RootPolicy{Owner: 0, Mode: 0700})
		if err != nil {
			return err
		}
		fs, err := platform.NewPrivateFSFromRoot(logRoot)
		if err != nil {
			return errors.Join(err, logRoot.Close())
		}
		token, err := credential.LoadOrCreateOwned(ctx, layout, locks)
		if err != nil {
			return errors.Join(err, fs.Close())
		}
		return runDaemonWith(ctx, daemonRunDeps{Paths: layout.Data, PrivateFS: fs, Token: token, Version: version, Endpoint: layout.ControlEndpoint, ValidationMode: true, ValidationReady: ready, ActivationPhase: app.InstallPhaseDefinitionCommitted,
			Listen: func(ctx context.Context) (net.Listener, error) { return transport.ListenOwned(ctx, layout, locks) },
			RuntimeOptions: app.RuntimeBuildOptions{ValidationCore: provenance, Resources: subscription.NewResourcePreparer(providers, nil, nil), RootConfigInput: func(_ context.Context, _ subscription.Document, settings config.Settings) (subscription.PolicyInput, error) {
				return subscription.PolicyInput{CoreTag: "v1.19.30", OS: runtime.GOOS, Arch: runtime.GOARCH, Settings: settings}, nil
			}},
		})
	})
}
