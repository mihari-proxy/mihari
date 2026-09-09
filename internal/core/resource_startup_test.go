package core_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/platform"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
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
func startupSettings(t *testing.T) config.Settings {
	t.Helper()
	s := config.Defaults()
	s.ControllerSecret = strings.Repeat("a", 64)
	addrs := []*string{&s.MixedAddr, &s.ControllerAddr, &s.WebAddr}
	for _, addr := range addrs {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		*addr = listener.Addr().String()
		if err = listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return s
}
func startupSourceFixture(t *testing.T) (*core.TestTrustedFixture, platform.Paths, string, []byte) {
	t.Helper()
	paths := platform.NewPaths(t.TempDir())
	paths.CoreBinary = filepath.Join(paths.Bin, "mihomo")
	f := core.NewTestTrustedFixture(t, paths.Root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	catalog := subscription.Defaults()
	catalog.ActiveID = id
	catalog.Profiles = []subscription.Profile{{ID: id, Name: "synthetic", URL: "https://example.invalid/source", Enabled: true, Generation: 7}}
	if err := subscription.Save(paths.SubscriptionCatalog, catalog); err != nil {
		t.Fatal(err)
	}
	raw := []byte("proxy-providers:\n  native:\n    type: file\n    path: ./legacy/native.yaml\n    x-extra: {nested: [preserved]}\ntun:\n  enable: true\n  stack: gvisor\n  x-extra: {route: native}\ngeodata-loader: memconservative\nrules: ['MATCH,DIRECT']\n")
	if err := os.WriteFile(filepath.Join(paths.SubscriptionCache, id+".yaml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return f, paths, id, raw
}
func TestRootAssembly_ResourceRecoveryPrecedesBusinessStoreLoad(t *testing.T) {
	f, paths, id, raw := startupSourceFixture(t)
	settings := startupSettings(t)
	catalog, err := os.ReadFile(paths.SubscriptionCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(paths.SubscriptionCatalog, []byte("interrupted: ["), 0600); err != nil {
		t.Fatal(err)
	}
	stale := settings.Clone()
	stale.MixedAddr = "127.0.0.1:1"
	recovered := false
	probe := recoveredStateProbe{settings: settings, startupResourcesProbe: startupResourcesProbe{recover: func() error {
		recovered = true
		if err := os.WriteFile(paths.SubscriptionCatalog, catalog, 0600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(paths.SubscriptionCache, id+".yaml"), raw, 0600)
	}}}
	doc, err := subscription.ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := subscription.Generate(doc, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	executions := 0
	f.Execute = func(_ context.Context, c core.CoreCommand) ([]byte, error) {
		executions++
		if !recovered {
			t.Fatal("executor preceded recovery")
		}
		if c.Args[0] == "-t" && !bytes.Equal(f.CommandConfig(c), expected) {
			t.Fatal("executor consumed stale settings/source")
		}
		return []byte("Mihomo v1.19.30"), nil
	}
	_, err = app.BuildRuntimeWithOptions(paths, stale, "test", nil, nil, app.RuntimeBuildOptions{TrustedCore: f.Trusted, Resources: probe})
	if err != nil {
		t.Fatal(err)
	}
	if executions == 0 || !bytes.Equal(f.Content(), expected) {
		t.Fatal("recovered tuple not published")
	}
}
func TestRootAssembly_DaemonRestartConvergesPersistedSourceAndSettings(t *testing.T) {
	// These represent separate-file crash windows: source bytes may precede
	// generation metadata, metadata/settings may precede derived config, and
	// settings may precede onboarding completion. No cross-file atomicity claimed.
	for _, window := range []string{"cache-before-catalog", "catalog-before-config", "settings-before-config", "settings-before-onboarding"} {
		t.Run(window, func(t *testing.T) {
			f, paths, id, raw := startupSourceFixture(t)
			settings := startupSettings(t)
			resources := map[string][]byte{}
			for _, name := range []string{"providers/old-managed.yaml", "Country.mmdb", "GeoSite.dat"} {
				p := filepath.Join(paths.Root, "runtime/core-home", name)
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				b := []byte("historical " + name)
				if err := os.WriteFile(p, b, 0600); err != nil {
					t.Fatal(err)
				}
				resources[p] = b
			}
			if window == "cache-before-catalog" {
				raw = append(raw, []byte("x-crash-source: updated\n")...)
				if err := os.WriteFile(filepath.Join(paths.SubscriptionCache, id+".yaml"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if window == "catalog-before-config" {
				catalog, err := subscription.Load(paths.SubscriptionCatalog)
				if err != nil {
					t.Fatal(err)
				}
				catalog.Profiles[0].Generation++
				if err = subscription.Save(paths.SubscriptionCatalog, catalog); err != nil {
					t.Fatal(err)
				}
			}
			settings.Tun = map[string]any{"enable": false, "stack": "ignored-old-setting"}
			if _, err := config.SaveWithCommit(paths.Settings, settings); err != nil {
				t.Fatal(err)
			}
			if window == "settings-before-onboarding" {
				if err := os.WriteFile(paths.Onboarding, []byte(`{"schema":"mihari.onboarding/v1","complete":false}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			doc, err := subscription.ParseDocument(raw)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := subscription.Generate(doc, nil, settings)
			if err != nil {
				t.Fatal(err)
			}
			f.Execute = func(_ context.Context, c core.CoreCommand) ([]byte, error) {
				if c.Args[0] == "-t" && !bytes.Equal(f.CommandConfig(c), expected) {
					t.Fatal("startup did not regenerate from persisted authority")
				}
				return []byte("Mihomo v1.19.30"), nil
			}
			_, err = app.BuildRuntimeWithOptions(paths, settings, "test", nil, nil, app.RuntimeBuildOptions{TrustedCore: f.Trusted})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(f.Content(), expected) {
				t.Fatal("old derived config reused")
			}
			actual, err := subscription.ParseDocument(f.Content())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual["proxy-providers"], doc["proxy-providers"]) {
				t.Fatal("native file provider rewritten")
			}
			tun := actual["tun"].(subscription.Document)
			if tun["stack"] != "gvisor" || tun["enable"] != false {
				t.Fatal("source TUN fields overwritten")
			}
			for p, want := range resources {
				got, err := os.ReadFile(p)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatal("historical resource changed", err)
				}
			}
		})
	}
}
func TestRootAssembly_InvalidSourceOrCandidatePreservesLastValidData(t *testing.T) {
	for _, failure := range []string{"missing-cache", "invalid-cache", "core-rejection"} {
		t.Run(failure, func(t *testing.T) {
			f, paths, id, _ := startupSourceFixture(t)
			settings := startupSettings(t)
			path := filepath.Join(paths.SubscriptionCache, id+".yaml")
			if failure == "missing-cache" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "invalid-cache" {
				if err := os.WriteFile(path, []byte("bad: ["), 0600); err != nil {
					t.Fatal(err)
				}
			}
			old := f.Content()
			catalog, err := os.ReadFile(paths.SubscriptionCatalog)
			if err != nil {
				t.Fatal(err)
			}
			executions := 0
			f.Execute = func(context.Context, core.CoreCommand) ([]byte, error) {
				executions++
				return nil, errors.New("synthetic refusal")
			}
			_, err = app.BuildRuntimeWithOptions(paths, settings, "test", nil, nil, app.RuntimeBuildOptions{TrustedCore: f.Trusted})
			if err == nil {
				t.Fatal("invalid authority accepted")
			}
			if failure != "core-rejection" {
				var api protocol.APIError
				if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || executions != 0 {
					t.Fatalf("source failure reached executor: %v calls=%d", err, executions)
				}
			}
			if !bytes.Equal(f.Content(), old) {
				t.Fatal("last valid config changed")
			}
			after, readErr := os.ReadFile(paths.SubscriptionCatalog)
			if readErr != nil || !bytes.Equal(after, catalog) {
				t.Fatal("catalog changed")
			}
		})
	}
}

func TestRootAssembly_OnboardingEndpointsApplyAtDaemonRestart(t *testing.T) {
	f, paths, _, raw := startupSourceFixture(t)
	old := startupSettings(t)
	if err := config.Save(paths.Settings, old); err != nil {
		t.Fatal(err)
	}
	assembly, err := app.BuildRuntimeWithOptions(paths, old, "test", nil, nil, app.RuntimeBuildOptions{TrustedCore: f.Trusted})
	if err != nil {
		t.Fatal(err)
	}
	before := f.Content()
	next := startupSettings(t)
	result, err := assembly.Manager.UpdateOnboarding(context.Background(), runtimeapi.Operation{ID: "endpoint-update", Source: "test"}, onboarding.Update{MixedAddr: &next.MixedAddr, ControllerAddr: &next.ControllerAddr, WebAddr: &next.WebAddr})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Status.RestartRequired || !bytes.Equal(before, f.Content()) {
		t.Fatal("endpoint update must await daemon restart")
	}
	saved, err := config.Load(paths.Settings)
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.BuildRuntimeWithOptions(paths, saved, "test", nil, nil, app.RuntimeBuildOptions{TrustedCore: f.Trusted})
	if err != nil {
		t.Fatal(err)
	}
	document, err := subscription.ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := subscription.Generate(document, nil, saved)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expected, f.Content()) {
		t.Fatal("daemon restart did not regenerate persisted endpoints and original fields")
	}
}
