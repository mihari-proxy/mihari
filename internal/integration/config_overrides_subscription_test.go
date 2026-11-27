package integration

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"go.yaml.in/yaml/v3"
)

func TestConfigOverridesIPC_RefreshAndSwitchUseOverridesWithoutChangingSource(t *testing.T) {
	root := t.TempDir()
	subs, err := subscription.Open(subscription.ServiceOptions{CatalogPath: filepath.Join(root, "catalog.yaml"), CacheDir: filepath.Join(root, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(root, "running.yaml")
	if err := os.WriteFile(active, []byte("proxies: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f := newOperationDiagnosticsIPCFixture(t, config.CommitResult{Committed: true}, nil, func(o *runtime.Options) {
		o.Store.Store(state.Snapshot{Core: state.CoreState{Status: "running", PID: 42}})
		o.Settings.ControllerSecret = strings.Repeat("ab", 32)
		o.Controller = &editController{}
		o.Logging = nil
		o.RuntimeConfig = active
		o.StagingDir = filepath.Join(root, "staging")
		o.Subscriptions = subs
	})
	source := func(name, resolver string) (string, string) {
		t.Helper()
		path := filepath.Join(root, name+".yaml")
		raw := "proxies: []\ndns:\n  nameserver: [" + resolver + "]\n"
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		uri, err := platform.FileURI(path)
		if err != nil {
			t.Fatal(err)
		}
		return uri, raw
	}
	add := func(name, resolver string) string {
		t.Helper()
		uri, _ := source(name, resolver)
		added, err := f.client.AddSubscription(t.Context(), protocol.SubscriptionAddRequest{OperationID: "add-" + name, Name: name, URL: uri})
		if err != nil {
			t.Fatal(err)
		}
		return added.Subscription.ID
	}
	assertDNS := func(resolver string) {
		t.Helper()
		body, err := os.ReadFile(active)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			DNS struct {
				Enable     bool
				Nameserver []string
			}
		}
		if err := yaml.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if !got.DNS.Enable || !slices.Equal(got.DNS.Nameserver, []string{resolver, "9.9.9.9"}) {
			t.Fatalf("effective DNS=%+v", got.DNS)
		}
	}
	first := add("first", "1.1.1.1")
	second := add("second", "2.2.2.2")
	if _, err = f.client.UseSubscription(t.Context(), first, protocol.MutationRequest{OperationID: "use-first"}); err != nil {
		t.Fatal(err)
	}
	_, err = f.client.UpdateConfigOverrides(t.Context(), "fragments", protocol.ConfigOverridesUpdateRequest{OperationID: "fragment", Fragments: []protocol.OverrideFragment{{ID: "extra", Name: "Extra resolver", Enabled: true, YAML: "dns: {nameserver+: [9.9.9.9]}"}}})
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	_, err = f.client.UpdateConfigOverrides(t.Context(), "dns", protocol.ConfigOverridesUpdateRequest{OperationID: "dns", DNS: map[string]protocol.DNSFieldOverride{"enable": {Operation: "set", Bool: &yes}}})
	if err != nil {
		t.Fatal(err)
	}
	assertDNS("1.1.1.1")
	_, updated := source("first", "3.3.3.3")
	if _, err = f.client.RefreshSubscription(t.Context(), first, protocol.MutationRequest{OperationID: "refresh-first"}); err != nil {
		t.Fatal(err)
	}
	assertDNS("3.3.3.3")
	cache, _, err := subs.ReadCache(first)
	if err != nil || string(cache) != updated {
		t.Fatalf("cache changed by override: %q %v", cache, err)
	}
	if _, err = f.client.UseSubscription(t.Context(), second, protocol.MutationRequest{OperationID: "use-second"}); err != nil {
		t.Fatal(err)
	}
	assertDNS("2.2.2.2")
	status, err := f.client.ConfigOverrides(t.Context())
	if err != nil || status.ActiveID != second || status.ActiveName != "second" {
		t.Fatalf("switched editor status: %+v %v", status, err)
	}
}

func TestConfigOverridesIPC_SaveWarningReplaysWithoutSecondWrite(t *testing.T) {
	f := newOperationDiagnosticsIPCFixture(t, config.CommitResult{Committed: true, Warning: errors.New("fixture DNS settings directory sync")}, nil, func(o *runtime.Options) {
		o.Store.Store(state.Snapshot{Core: state.CoreState{Status: "stopped"}})
		o.BinaryExists = func() bool { return false }
		o.Settings.ControllerSecret = strings.Repeat("ab", 32)
	})
	req := protocol.ConfigOverridesUpdateRequest{OperationID: "dns-warning", DNS: map[string]protocol.DNSFieldOverride{"nameserver": {Operation: "set", Servers: []string{"1.1.1.1"}}}}
	for range 2 {
		got, err := f.client.UpdateConfigOverrides(t.Context(), "dns", req)
		if err != nil || got.Revision != 1 || !slices.Equal(got.Config.DNS["nameserver"].Servers, req.DNS["nameserver"].Servers) {
			t.Fatalf("committed DNS changed outcome: %+v %v", got, err)
		}
		if len(got.Warnings) != 1 || got.Warnings[0].Diagnostic == nil || !strings.Contains(got.Warnings[0].Diagnostic.Detail, "fixture DNS settings directory sync") {
			t.Fatalf("warning lost: %+v", got.Warnings)
		}
	}
	if f.saver.CallCount() != 1 {
		t.Fatalf("saves=%d want 1", f.saver.CallCount())
	}
}
