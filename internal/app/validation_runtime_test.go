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

func TestInstallValidation_AssemblyPreservesNativeExtensions(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(paths.SubscriptionCache, id+".yaml"), []byte("proxies: []\nproxy-groups: []\nrules: ['MATCH,DIRECT']\nunknown-field: {nested: [native, extension]}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	_, err := BuildRuntimeWithOptions(paths, settings, "test", nil, nil, RuntimeBuildOptions{ValidationMode: true})
	if err != nil {
		t.Fatalf("native extensions rejected: %v", err)
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

type validationRecoveryProbe struct {
	recover func() (*config.Settings, error)
}

func (p validationRecoveryProbe) Recover(context.Context) error { _, err := p.recover(); return err }
func (p validationRecoveryProbe) RecoverState(context.Context) (*config.Settings, error) {
	return p.recover()
}

func TestInstallValidation_RecoveryPrecedesStoresAndPropagatesSettings(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(paths.SubscriptionCatalog), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SubscriptionCatalog, []byte("interrupted: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Onboarding, []byte("interrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	settings.MixedAddr = "127.0.0.1:19238"
	settings.Tun = map[string]any{"enable": true}
	probe := validationRecoveryProbe{recover: func() (*config.Settings, error) {
		if err := subscription.Save(paths.SubscriptionCatalog, subscription.Defaults()); err != nil {
			return nil, err
		}
		if err := os.Remove(paths.Onboarding); err != nil {
			return nil, err
		}
		return &settings, nil
	}}
	assembly, err := BuildValidationRuntime(context.Background(), paths, config.Defaults(), "test", RuntimeBuildOptions{Resources: probe})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := assembly.Manager.TunStatus(context.Background())
	if err != nil || !actual.DesiredEnable {
		t.Fatal("validation assembly used pre-recovery settings", err)
	}
	if !assembly.SetupRequired {
		t.Fatal("recovered missing onboarding should require setup")
	}
}
func TestInstallValidation_ActiveMissingCacheFails(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(paths.SubscriptionCatalog), 0700); err != nil {
		t.Fatal(err)
	}
	catalog := subscription.Defaults()
	catalog.ActiveID = "11111111111111111111111111111111"
	catalog.Profiles = []subscription.Profile{{ID: catalog.ActiveID, Name: "missing-cache", URL: "https://example.invalid/sub", Enabled: true, Generation: 1}}
	if err := subscription.Save(paths.SubscriptionCatalog, catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildValidationRuntime(context.Background(), paths, config.Defaults(), "test", RuntimeBuildOptions{}); err == nil {
		t.Fatal("active uncached profile accepted")
	}
}
