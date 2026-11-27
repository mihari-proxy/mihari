package integration

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

func TestConfigOverridesIPC_PersistsAndRebuildsOnNewManager(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihari.yaml")
	newOptions := func(o *runtimeapi.Options) {
		o.Store.Store(state.Snapshot{Core: state.CoreState{Status: "stopped"}})
		o.Settings.ControllerSecret = strings.Repeat("ab", 32)
		o.BinaryExists = func() bool { return false }
		o.SettingsPath = path
		o.SaveSettings = config.SaveWithCommit
	}
	first := newOperationDiagnosticsIPCFixture(t, config.CommitResult{Committed: true}, nil, newOptions)
	ctx := t.Context()
	initial, err := first.client.ConfigOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fragments := []protocol.OverrideFragment{{ID: "company", Name: "Company", Enabled: true, YAML: "# company overrides\nauthentication: ['user:fixture-pass']\ndns: {enable: true, nameserver: [1.1.1.1]}"}}
	saved, err := first.client.UpdateConfigOverrides(ctx, "fragments", protocol.ConfigOverridesUpdateRequest{OperationID: "save-frags", IfRevision: &initial.Revision, Fragments: fragments})
	if err != nil {
		t.Fatal(err)
	}
	dns := map[string]protocol.DNSFieldOverride{"nameserver": {Operation: "set", Servers: []string{"9.9.9.9"}}}
	_, err = first.client.UpdateConfigOverrides(ctx, "dns", protocol.ConfigOverridesUpdateRequest{OperationID: "stale", IfRevision: &initial.Revision, DNS: dns})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("stale write: %v", err)
	}
	_, err = first.client.UpdateConfigOverrides(ctx, "dns", protocol.ConfigOverridesUpdateRequest{OperationID: "save-dns", IfRevision: &saved.Revision, DNS: dns})
	if err != nil {
		t.Fatal(err)
	}
	old, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	second := newOperationDiagnosticsIPCFixture(t, config.CommitResult{Committed: true}, nil, func(o *runtimeapi.Options) { newOptions(o); o.Settings = old })
	loaded, err := second.client.ConfigOverrides(ctx)
	if err != nil || !reflect.DeepEqual(loaded.Config.Fragments, fragments) {
		t.Fatalf("reopened=%+v err=%v", loaded.Config, err)
	}
	got, err := second.client.PreviewConfigOverrides(ctx, protocol.ConfigOverridesPreviewRequest{Category: "dns", DNS: loaded.Config.DNS})
	if err != nil {
		t.Fatal(err)
	}
	seenDNS := false
	for _, l := range got.Lines {
		if strings.Contains(l.Text, "9.9.9.9") && l.Source == "DNS form" {
			seenDNS = true
		}
	}
	if !seenDNS {
		t.Fatalf("preview=%+v", got.Lines)
	}
	auth, err := second.client.ProxyAuth(ctx, false)
	if err != nil || len(auth.Users) != 1 || auth.Users[0].Username != "user" || auth.Users[0].Password != "" || auth.Users[0].BaseSource != "Company" {
		t.Fatalf("auth=%+v err=%v", auth, err)
	}
}

func TestConfigOverridesPreview_LargeScalarAndInheritedSource(t *testing.T) {
	dir := t.TempDir()
	service, err := subscription.Open(subscription.ServiceOptions{CatalogPath: filepath.Join(dir, "catalog.yaml"), CacheDir: filepath.Join(dir, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("c", 32)
	cat := subscription.Defaults()
	cat.ActiveID = id
	cat.Profiles = []subscription.Profile{{ID: id, Name: "Office", URL: "https://fixture.invalid/sub", Enabled: true, Generation: 1}}
	if err = service.Restore(cat); err != nil {
		t.Fatal(err)
	}
	const count = 1200000
	value := strings.Repeat("界", count)
	if err = os.WriteFile(service.CachePath(id), []byte("proxies: []\ndns: {nameserver: [1.1.1.1], ipv6: true}\nx-native: "+value+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f := newOperationDiagnosticsIPCFixture(t, config.CommitResult{Committed: true}, nil, func(o *runtimeapi.Options) {
		o.Settings.ControllerSecret = strings.Repeat("ab", 32)
		o.Subscriptions = service
	})
	req := protocol.ConfigOverridesPreviewRequest{Category: "dns", Full: true, DNS: map[string]protocol.DNSFieldOverride{"nameserver": {Operation: "set", Servers: []string{"9.9.9.9"}}}}
	var reconstructed strings.Builder
	pages := 0
	for {
		page, err := f.client.PreviewConfigOverrides(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, l := range page.Lines {
			if l.Field == "x-native" && l.Change != "removed" {
				reconstructed.WriteString(l.Text)
			}
			if l.Field == "dns.ipv6" && l.Source != "Office" {
				t.Errorf("inherited IPv6 mislabeled: %+v", l)
			}
		}
		if page.NextOffset == 0 {
			break
		}
		if page.NextOffset <= req.Offset {
			t.Fatal("preview did not advance")
		}
		req.Offset, req.Snapshot = page.NextOffset, page.Snapshot
	}
	if pages < 2 || reconstructed.String() != "x-native: "+value {
		t.Fatalf("incomplete scalar (%d pages, %d bytes)", pages, reconstructed.Len())
	}
	req.Reveal = true
	_, err = f.client.PreviewConfigOverrides(t.Context(), req)
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("mismatched cursor: %v", err)
	}
}
