package app

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallValidation_AssemblyRejectsCorruptCatalog(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(paths.SubscriptionCatalog), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SubscriptionCatalog, []byte("schema: invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRuntimeWithOptions(paths, config.Defaults(), "test", nil, nil, RuntimeBuildOptions{ValidationMode: true}); err == nil {
		t.Fatal("corrupt catalog accepted as ready")
	}
}

func TestInstallValidation_AssemblyRejectsRootPolicy(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	id := "11111111111111111111111111111111"
	if err := os.MkdirAll(paths.SubscriptionCache, 0700); err != nil {
		t.Fatal(err)
	}
	catalog := subscription.Defaults()
	catalog.Profiles = []subscription.Profile{{ID: id, Name: "test", URL: "https://example.invalid/sub", Enabled: true, Generation: 1, ProxyMode: subscription.ProxyModeDirect}}
	catalog.ActiveID = id
	if err := subscription.Save(paths.SubscriptionCatalog, catalog); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.SubscriptionCache, id+".yaml"), []byte("proxies: []\nproxy-groups: []\nrules: [MATCH,DIRECT]\nunknown-field: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err := BuildRuntimeWithOptions(paths, config.Defaults(), "test", nil, nil, RuntimeBuildOptions{ValidationMode: true, RootConfigInput: func(context.Context, subscription.Document, config.Settings) (subscription.PolicyInput, error) {
		called = true
		return subscription.PolicyInput{CoreTag: "v1.19.30", OS: "linux", Arch: "amd64"}, nil
	}})
	if err == nil || !called {
		t.Fatalf("root policy bypassed: err=%v called=%v", err, called)
	}
}

func TestInstallValidation_SetupRequiredReadOnly(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	assembly, err := BuildRuntimeWithOptions(paths, config.Defaults(), "test", nil, nil, RuntimeBuildOptions{ValidationMode: true, InitialSetupRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	if !assembly.SetupRequired {
		t.Fatal("missing core/onboarding not reported as setup_required")
	}
	entries, err := os.ReadDir(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("validation created business files: %v", entries)
	}
}

func TestInstallValidation_AssemblyCancellationBeforeIO(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := BuildValidationRuntime(ctx, platform.NewPaths(t.TempDir()), config.Defaults(), "test", RuntimeBuildOptions{})
	if err != context.Canceled {
		t.Fatalf("canceled validation kept reading objects: %v", err)
	}
}
