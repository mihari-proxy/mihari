package runtime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type authPreviewAPI interface {
	PreviewProxyAuth(context.Context, protocol.ProxyAuthPreviewRequest) (protocol.ProxyAuthPreview, error)
}

func TestProxyAuthPreview_UsesOriginalSubscriptionAfterDisabledAndCustomModes(t *testing.T) {
	m, core := onlineEgressManager(t)
	root := t.TempDir()
	service, err := subscription.Open(subscription.ServiceOptions{CatalogPath: filepath.Join(root, "catalog.yaml"), CacheDir: filepath.Join(root, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("b", 32)
	catalog := subscription.Defaults()
	catalog.ActiveID = id
	catalog.Profiles = []subscription.Profile{{ID: id, Name: "fixture", URL: "https://example.invalid/sub", Enabled: true, Generation: 1}}
	if err := service.Restore(catalog); err != nil {
		t.Fatal(err)
	}
	source := []byte("proxies: []\nauthentication:\n  - alice:source-secret\n  - native-record-without-colon\nskip-auth-prefixes:\n  - 192.0.2.0/24\nsecret: unrelated-controller-secret\n")
	if err := os.WriteFile(service.CachePath(id), source, 0600); err != nil {
		t.Fatal(err)
	}
	m.subscriptions = service
	m.settingsMu.Lock()
	m.settings.SetProxyAuth(protocol.ProxyAuthConfig{Mode: "disabled", BypassMode: "custom", Users: []protocol.ProxyAuthUser{{Username: "alice", Password: "saved-secret"}}, BypassPrefixes: []string{"198.51.100.0/24"}})
	m.settingsMu.Unlock()
	for _, mode := range []string{"follow", "enabled"} {
		request := protocol.ProxyAuthPreviewRequest{Reveal: true, Config: protocol.ProxyAuthConfig{Mode: mode, BypassMode: "follow"}}
		got, err := m.PreviewProxyAuth(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		var candidate strings.Builder
		for _, line := range got.Lines {
			if line.Change != "-" {
				candidate.WriteString(line.Text + "\n")
			}
		}
		if got.ActiveID != id || !strings.Contains(candidate.String(), "alice:source-secret") || !strings.Contains(candidate.String(), "192.0.2.0/24") || strings.Contains(candidate.String(), "saved-secret") || strings.Contains(candidate.String(), "unrelated-controller-secret") {
			t.Fatalf("mode=%s preview=%s", mode, candidate.String())
		}
		if mode == "follow" && !strings.Contains(candidate.String(), "native-record-without-colon") {
			t.Fatal("follow preview lost native source records")
		}
	}
	after, err := os.ReadFile(service.CachePath(id))
	if err != nil || string(after) != string(source) || core.reloads != 0 {
		t.Fatal("preview wrote the source or applied config")
	}
	if !slices.Contains(m.Capabilities(), protocol.CapabilityProxyAuthPreview) {
		t.Fatal("preview capability not advertised")
	}
}

func TestProxyAuthPreview_CanceledRequestDoesNotResolve(t *testing.T) {
	m := startupLoggingManager(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.PreviewProxyAuth(ctx, protocol.ProxyAuthPreviewRequest{}); err == nil {
		t.Fatal("canceled preview succeeded")
	}
}

func TestProxyAuthPreview_ReadOnlyAndMaskedDiff(t *testing.T) {
	m, core := onlineEgressManager(t)
	api, ok := any(m).(authPreviewAPI)
	if !ok {
		t.Fatal("read-only proxy auth preview is missing")
	}
	m.settingsMu.Lock()
	m.settings.SetProxyAuth(protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "required", Users: []protocol.ProxyAuthUser{{Username: "fixture", Password: "old-secret"}}})
	m.settingsMu.Unlock()
	before, snapshot := m.settingsSnapshot(), m.store.Load()
	request := protocol.ProxyAuthPreviewRequest{Config: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "custom", LocalBypass: true, Users: []protocol.ProxyAuthUser{{Username: "fixture", Password: "new-secret"}}}}
	got, err := api.PreviewProxyAuth(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	changes := map[string]bool{}
	for _, line := range got.Lines {
		text.WriteString(line.Text)
		changes[line.Change] = true
	}
	if strings.Contains(text.String(), "secret") || !strings.Contains(text.String(), "fixture:********") || !changes["+"] || !changes["-"] || !strings.Contains(text.String(), "127.0.0.0/8") {
		t.Fatalf("unexpected preview: %+v", got)
	}
	if core.reloads != 0 || core.closed != 0 || !reflect.DeepEqual(before, m.settingsSnapshot()) || !reflect.DeepEqual(snapshot, m.store.Load()) {
		t.Fatal("preview changed runtime state")
	}
	request.Reveal = true
	got, err = api.PreviewProxyAuth(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	text.Reset()
	for _, line := range got.Lines {
		text.WriteString(line.Text)
	}
	if !strings.Contains(text.String(), "old-secret") || !strings.Contains(text.String(), "new-secret") {
		t.Fatal("explicit reveal lost diff credentials")
	}
}

func TestProxyAuthPreview_MissingFieldsBlankPasswordAndInvalidDraft(t *testing.T) {
	m := startupLoggingManager(t)
	api, ok := any(m).(authPreviewAPI)
	if !ok {
		t.Fatal("read-only proxy auth preview is missing")
	}
	got, err := api.PreviewProxyAuth(t.Context(), protocol.ProxyAuthPreviewRequest{Config: config.Defaults().EffectiveProxyAuth()})
	if err != nil || got.ActiveID != "" || len(got.Lines) != 2 || got.Lines[0].Text != "# authentication: not set" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	request := protocol.ProxyAuthPreviewRequest{Config: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "required", Users: []protocol.ProxyAuthUser{{Username: "fixture"}}}}
	if _, err := api.PreviewProxyAuth(t.Context(), request); err == nil {
		t.Fatal("invalid draft was accepted")
	}
	m.settingsMu.Lock()
	m.settings.SetProxyAuth(protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "required", Users: []protocol.ProxyAuthUser{{Username: "fixture", Password: "keep-secret"}}})
	m.settingsMu.Unlock()
	request.Reveal = true
	got, err = api.PreviewProxyAuth(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range got.Lines {
		if strings.Contains(line.Text, "keep-secret") {
			found = true
		}
		if line.Change != "" {
			t.Fatal("blank password changed existing credential")
		}
	}
	if !found {
		t.Fatal("existing password was not preserved")
	}
}
