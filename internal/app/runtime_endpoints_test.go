package app

import (
	"net"
	"os"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/platform"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"go.yaml.in/yaml/v3"
)

func TestBuildRuntime_EndpointUpdateSurvivesRestart(t *testing.T) {
	for _, field := range []string{"mixed IP", "mixed port", "controller", "web"} {
		t.Run(field, func(t *testing.T) {
			paths := platform.NewPaths(t.TempDir())
			settings := testRuntimeSettings(t)
			settings.ControllerSecret = strings.Repeat("a", 64)
			assembly, err := BuildRuntime(paths, settings, "test", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(paths.RuntimeConfig)
			if err != nil {
				t.Fatal(err)
			}
			// Preserve subscription content while refreshing only managed endpoints.
			original = append(original, []byte("ipv6: true\n")...)
			if err := config.AtomicWrite(paths.RuntimeConfig, original, 0o600); err != nil {
				t.Fatal(err)
			}
			next := testRuntimeSettings(t)
			update := onboarding.Update{}
			switch field {
			case "mixed IP":
				_, port, err := net.SplitHostPort(settings.MixedAddr)
				if err != nil {
					t.Fatal(err)
				}
				address := net.JoinHostPort("::1", port)
				update.MixedAddr = &address
			case "mixed port":
				update.MixedAddr = &next.MixedAddr
			case "controller":
				update.ControllerAddr = &next.ControllerAddr
			case "web":
				update.WebAddr = &next.WebAddr
			}
			status, err := assembly.Manager.UpdateOnboarding(t.Context(), runtimeapi.Operation{ID: "update-endpoint", Source: "tui"}, update)
			if err != nil {
				t.Fatal(err)
			}
			if !status.Status.RestartRequired {
				t.Fatal("missing restart marker")
			}
			persisted, err := config.Load(paths.Settings)
			if err != nil {
				t.Fatal(err)
			}
			if update.MixedAddr != nil && persisted.MixedAddr != *update.MixedAddr {
				t.Fatalf("saved mixed endpoint=%q want=%q", persisted.MixedAddr, *update.MixedAddr)
			}
			if update.ControllerAddr != nil && persisted.ControllerAddr != *update.ControllerAddr {
				t.Fatalf("saved controller endpoint=%q want=%q", persisted.ControllerAddr, *update.ControllerAddr)
			}
			if update.WebAddr != nil && persisted.WebAddr != *update.WebAddr {
				t.Fatalf("saved web endpoint=%q want=%q", persisted.WebAddr, *update.WebAddr)
			}
			restarted, err := BuildRuntime(paths, persisted, "test", nil, nil)
			if err != nil {
				t.Fatalf("restart after successful endpoint update: %v", err)
			}
			if restarted.Store.Load().Health != "ok" {
				t.Fatal("restart degraded")
			}
			if err := core.EnsureRuntimeConfig(paths.RuntimeConfig, persisted); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(paths.RuntimeConfig)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := yaml.Unmarshal(content, &document); err != nil {
				t.Fatal(err)
			}
			if document["ipv6"] != true {
				t.Fatal("unrelated configuration lost")
			}
		})
	}
}
