package core_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type recoveredStateProbe struct {
	startupResourcesProbe
	settings config.Settings
}

func (p recoveredStateProbe) RecoverState(ctx context.Context) (*config.Settings, error) {
	if err := p.Recover(ctx); err != nil {
		return nil, err
	}
	return &p.settings, nil
}

func TestRootAssembly_ResourceRecoveryPrecedesBusinessStoreLoad(t *testing.T) {
	root := t.TempDir()
	fixture := core.NewTestTrustedFixture(t, root)
	paths := platform.NewPaths(root)
	paths.CoreBinary = filepath.Join(paths.Bin, "mihomo")
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	// A store opened before recovery cannot even parse this interrupted value.
	if err := os.WriteFile(paths.SubscriptionCatalog, []byte("interrupted: ["), 0600); err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	old := config.Defaults()
	old.ControllerSecret = strings.Repeat("a", 64)
	stale := old.Clone()
	stale.WebAddr = "127.0.0.1:9998"
	stop := errors.New("offline reconstruction observed recovered tuple")
	probe := recoveredStateProbe{settings: old, startupResourcesProbe: startupResourcesProbe{
		recover: func() error {
			catalog := subscription.Defaults()
			catalog.ActiveID = id
			catalog.Profiles = []subscription.Profile{{ID: id, Name: "recovered", URL: "https://example.test/sub", Enabled: true, Generation: 7}}
			if err := subscription.Save(paths.SubscriptionCatalog, catalog); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(paths.SubscriptionCache, id+".yaml"), []byte("proxies: []\nrules: ['MATCH,DIRECT']\n"), 0600)
		},
		snapshot: func(input subscription.PolicyInput) (*subscription.ResourceGraph, error) {
			if input.SubscriptionID != id || input.Generation != 7 || input.Settings.WebAddr != old.WebAddr {
				t.Fatal("startup consumed stale settings/catalog")
			}
			return nil, stop
		},
	}}
	_, err := app.BuildRuntimeWithOptions(paths, stale, "test", nil, nil, app.RuntimeBuildOptions{
		TrustedCore: fixture.Trusted, Resources: probe,
		RootConfigInput: func(_ context.Context, _ subscription.Document, settings config.Settings) (subscription.PolicyInput, error) {
			if settings.WebAddr != old.WebAddr {
				t.Fatal("pre-recovery settings passed to config policy")
			}
			return core.FixturePolicyInput(), nil
		},
	})
	if !errors.Is(err, stop) {
		t.Fatalf("startup failed before recovered tuple was loaded: %v", err)
	}
}
