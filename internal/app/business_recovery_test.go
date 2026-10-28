package app

import (
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"go.yaml.in/yaml/v3"
	"os"
	"strings"
	"testing"
)

func TestBusinessRecovery_AuthResetRepairsOnlyAuthAndNeverStartsCore(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("ab", 32)
	settings.MixedAddr = "127.0.0.1:19390"
	settings.ProxyAuth = &protocol.ProxyAuthConfig{Mode: "invalid"}
	raw, err := yaml.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Settings, raw, 0600); err != nil {
		t.Fatal(err)
	}
	store := NewDegradedStore("fixture", os.ErrInvalid)
	m := NewBusinessRecovery(paths, store, nil)
	_, err = m.UpdateProxyAuth(t.Context(), runtimeapi.Operation{ID: "repair"}, protocol.ProxyAuthUpdateRequest{Reset: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(paths.Settings)
	if err != nil || got.ProxyAuth != nil || got.MixedAddr != settings.MixedAddr || got.ControllerSecret != settings.ControllerSecret {
		t.Fatalf("reset changed non-auth settings: %v", err)
	}
	if m.Snapshot().Core.PID != 0 {
		t.Fatal("recovery started a core")
	}
	if _, err = m.EnableSystemProxy(t.Context(), runtimeapi.Operation{ID: "forbidden"}, false); err == nil {
		t.Fatal("recovery allowed an unrelated business mutation")
	}
}
