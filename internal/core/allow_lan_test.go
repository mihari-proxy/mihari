package core

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/config"
	"go.yaml.in/yaml/v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReconcileRuntimeEndpoints_AllowLANTransitions(t *testing.T) {
	root := t.TempDir()
	path, binary := filepath.Join(root, "config.yaml"), filepath.Join(root, "core")
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	if err := WriteBootstrapConfig(path, settings); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("inert fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		settings.AllowLAN = enabled
		settings.MixedAddr = "127.0.0.1:9190"
		if enabled {
			settings.MixedAddr = "0.0.0.0:9190"
		}
		validated := false
		runner := endpointRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
			validated = true
			return nil, EnsureRuntimeConfig(args[4], settings)
		})
		if err := ReconcileRuntimeEndpoints(t.Context(), runner, binary, root, path, settings, nil); err != nil {
			t.Fatal(err)
		}
		if !validated {
			t.Fatal("LAN change was not validated")
		}
		if err := EnsureRuntimeConfig(path, settings); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := yaml.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		if document["allow-lan"] != enabled {
			t.Fatalf("document=%v", document)
		}
	}
}
