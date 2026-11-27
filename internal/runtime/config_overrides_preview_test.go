package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type draftOverridesReader interface {
	PreviewConfigOverrides(context.Context, protocol.ConfigOverridesPreviewRequest) (protocol.ConfigOverridesPreview, error)
}

func TestConfigOverridesPreviewFinalDraftAndSecrets(t *testing.T) {
	m, core := onlineEgressManager(t)
	api, ok := any(m).(draftOverridesReader)
	if !ok {
		t.Fatal("missing configuration override draft preview")
	}
	req := protocol.ConfigOverridesPreviewRequest{Category: "fragments", Full: true, Fragments: []protocol.OverrideFragment{{ID: "lab", Name: "Lab DNS", Enabled: true, YAML: "dns: {enable: true, nameserver: [1.1.1.1]}\nauthentication: ['alice:synthetic-preview-password']"}}}
	got, err := api.PreviewConfigOverrides(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1.1.1.1", "Lab DNS", "alice:", "MATCH,DIRECT"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("missing %s in %s", want, raw)
		}
	}
	if strings.Contains(string(raw), "synthetic-preview-password") || strings.Contains(string(raw), m.settingsSnapshot().ControllerSecret) || core.reloads != 0 {
		t.Fatal("preview exposed credentials or reloaded")
	}
	req.Reveal = true
	got, err = api.PreviewConfigOverrides(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "synthetic-preview-password") || strings.Contains(string(raw), m.settingsSnapshot().ControllerSecret) {
		t.Fatal("explicit password reveal changed controller visibility")
	}
}

func newPagedOverridePreview(t *testing.T, name string) (*Manager, *subscription.Service, string) {
	t.Helper()
	root := t.TempDir()
	service, err := subscription.Open(subscription.ServiceOptions{CatalogPath: filepath.Join(root, "catalog.yaml"), CacheDir: filepath.Join(root, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("b", 32)
	catalog := subscription.Defaults()
	catalog.ActiveID = id
	catalog.Profiles = []subscription.Profile{{ID: id, Name: name, URL: "https://fixture.invalid/source", Enabled: true, Generation: 1}}
	if err = service.Restore(catalog); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(service.CachePath(id), []byte("proxies: []\nx-native: "+strings.Repeat("<", 220000)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := startupLoggingManager(t)
	m.subscriptions = service
	return m, service, id
}

func TestConfigOverridesPreview_ContinuationRetainsRenderedSnapshot(t *testing.T) {
	m, subs, id := newPagedOverridePreview(t, "Office")
	req := protocol.ConfigOverridesPreviewRequest{Category: "dns", Full: true}
	first, err := m.PreviewConfigOverrides(t.Context(), req)
	if err != nil || first.NextOffset == 0 {
		t.Fatalf("missing pages: %v", err)
	}
	req.Offset, req.Snapshot = first.NextOffset, first.Snapshot
	// The next page can be read from the generated snapshot while the old source
	// bytes are unavailable. The committed input generation has not changed.
	if err = os.Remove(subs.CachePath(id)); err != nil {
		t.Fatal(err)
	}
	next, err := m.PreviewConfigOverrides(t.Context(), req)
	if err != nil || len(next.Lines) == 0 || next.Snapshot != first.Snapshot {
		t.Fatalf("continuation: %v", err)
	}
}

func TestConfigOverridesPreview_RenamedSubscriptionInvalidatesContinuation(t *testing.T) {
	m, subs, _ := newPagedOverridePreview(t, "Office")
	req := protocol.ConfigOverridesPreviewRequest{Category: "dns", Full: true}
	first, err := m.PreviewConfigOverrides(t.Context(), req)
	if err != nil || first.NextOffset == 0 {
		t.Fatalf("missing pages: %v", err)
	}
	req.Offset, req.Snapshot = first.NextOffset, first.Snapshot
	_, _, err = subs.Mutate(func(c *subscription.Catalog) error { c.Profiles[0].Name = "Travel"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.PreviewConfigOverrides(t.Context(), req)
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("pagination accepted renamed source: %v", err)
	}
}

func TestConfigOverridesPreview_LongSourceLabelFitsWireBudget(t *testing.T) {
	m, _, _ := newPagedOverridePreview(t, strings.Repeat("<", 720000))
	got, err := m.PreviewConfigOverrides(t.Context(), protocol.ConfigOverridesPreviewRequest{Category: "dns", Full: true})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil || len(encoded) > 1<<20 {
		t.Fatalf("preview page bytes=%d err=%v", len(encoded), err)
	}
}
