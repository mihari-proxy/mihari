package subscription

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/config"
	"go.yaml.in/yaml/v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyResourceState_FixedTargetAuthority(t *testing.T) {
	for _, role := range []resourceStateRole{resourceSettings, resourceCatalog, resourceOnboarding} {
		if _, err := stateTarget(role, legacySubscription); err == nil {
			t.Fatal("state accepted caller identity")
		}
	}
	for _, id := range []string{"../escape", "", "UPPERCASE000000000000000000000000"} {
		if _, err := stateTarget(resourceSourceCache, id); err == nil {
			t.Fatal("cache path escaped fixed identity")
		}
	}
}

type settingsRecoveryFiles struct {
	*memoryProviderFiles
	root string
}

func (f settingsRecoveryFiles) storeBinding() (string, string) { return f.root, "root-1" }
func (f settingsRecoveryFiles) move(ctx context.Context, from string, source providerObject, to string, target providerObject) error {
	if err := f.memoryProviderFiles.move(ctx, from, source, to, target); err != nil {
		return err
	}
	if to == "mihari.yaml" {
		return os.WriteFile(filepath.Join(f.root, "mihari.yaml"), f.objects[to], 0600)
	}
	return nil
}
func TestLegacyResourceRecovery_ReloadsRecoveredSettingsForUpstreamCallers(t *testing.T) {
	fs, _ := legacyResourceFixture(t, 60, false, false, true)
	before := config.Defaults()
	before.ControllerSecret = strings.Repeat("a", 64)
	before.Tun = map[string]any{"enable": false}
	after := before.Clone()
	after.Tun = map[string]any{"enable": true}
	oldRaw, err := yaml.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	newRaw, err := yaml.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	mutateLegacyResource(t, fs, func(_ map[string]any, entries []any) {
		entry := entries[6].(map[string]any)
		tx := entry["transaction"].(string)
		fs.objects["mihari.yaml.old-"+tx] = oldRaw
		fs.objects["mihari.yaml"] = newRaw
		entry["old"].(map[string]any)["sha256"] = providerDigest(oldRaw)
		entry["new"].(map[string]any)["sha256"] = providerDigest(newRaw)
	})
	root := t.TempDir()
	if err := config.Save(filepath.Join(root, "mihari.yaml"), after); err != nil {
		t.Fatal(err)
	}
	store := &ProviderStore{files: settingsRecoveryFiles{memoryProviderFiles: fs, root: root}}
	recovered, err := store.RecoverState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if recovered == nil || recovered.Tun["enable"] != false {
		t.Fatal("upstream caller retained settings loaded before recovery")
	}
	recovered, err = store.RecoverState(context.Background())
	if err != nil || recovered != nil {
		t.Fatal("settled recovery unexpectedly invalidated settings", err)
	}
}
