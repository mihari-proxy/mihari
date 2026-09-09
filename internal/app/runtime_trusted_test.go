package app

import (
	"bytes"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"os"
	"path/filepath"
	"testing"
)

func TestStartupConfig_NoActiveSubscriptionBuildsResourceFreeBootstrap(t *testing.T) {
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
			settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			generated, err := startupConfig(subs, settings)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(generated, []byte("MATCH,DIRECT")) {
				t.Fatal("bootstrap must route directly")
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
