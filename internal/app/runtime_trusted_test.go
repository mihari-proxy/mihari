package app

import (
	"bytes"
	"context"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"os"
	"path/filepath"
	"testing"
)

func TestStartupPolicyInput_NoActiveSubscriptionBuildsResourceFreeBootstrap(t *testing.T) {
	for _, inactiveProfile := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty-catalog", true: "inactive-uncached-profile"}[inactiveProfile], func(t *testing.T) {
			paths := platform.NewPaths(t.TempDir())
			if err := os.MkdirAll(filepath.Dir(paths.SubscriptionCatalog), 0700); err != nil {
				t.Fatal(err)
			}
			catalog := subscription.Defaults()
			if inactiveProfile {
				catalog.Profiles = []subscription.Profile{{ID: "11111111111111111111111111111111", Name: "uncached", URL: "https://example.invalid/sub", Enabled: true}}
			}
			if err := subscription.Save(paths.SubscriptionCatalog, catalog); err != nil {
				t.Fatal(err)
			}
			subs, err := subscription.Open(subscription.ServiceOptions{CatalogPath: paths.SubscriptionCatalog, CacheDir: paths.SubscriptionCache})
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(paths.SubscriptionCatalog)
			if err != nil {
				t.Fatal(err)
			}
			settings := config.Defaults()
			settings.ControllerSecret = "isolated-bootstrap-fixture"
			options := RuntimeBuildOptions{RootConfigInput: func(_ context.Context, document subscription.Document, _ config.Settings) (subscription.PolicyInput, error) {
				if document != nil {
					t.Fatal("bootstrap unexpectedly loaded a subscription")
				}
				// Match the production Unix assembly: it supplies the supported
				// core tuple, not a fabricated persisted subscription identity.
				return subscription.PolicyInput{CoreTag: "v1.19.30", OS: "linux", Arch: "amd64"}, nil
			}}
			input, err := startupPolicyInput(context.Background(), options, subs, settings)
			if err != nil {
				t.Fatal(err)
			}
			generated, err := subscription.NewRootConfigPolicy().Build(context.Background(), input)
			if err != nil {
				t.Fatalf("fresh startup policy rejected its bootstrap: %v", err)
			}
			if len(generated.Providers) != 0 || len(generated.Geo) != 0 || !bytes.Contains(generated.YAML, []byte("MATCH,DIRECT")) {
				t.Fatal("bootstrap must require no external resources and route directly")
			}
			after, err := os.ReadFile(paths.SubscriptionCatalog)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("bootstrap changed persisted subscription identity or generation", err)
			}
		})
	}
}

func TestBuildRuntime_RejectsUninitializedTrustedCapabilityBeforeIO(t *testing.T) {
	root := filepath.Join(t.TempDir(), "must-not-create")
	paths := platform.NewPaths(root)
	_, err := BuildRuntimeWithOptions(paths, config.Defaults(), "test", nil, nil, RuntimeBuildOptions{TrustedCore: &core.TrustedExecution{}})
	if err == nil {
		t.Fatal("uninitialized trusted runtime accepted")
	}
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("root assembly touched data before checking capability")
	}
}
