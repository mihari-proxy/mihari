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
	req := protocol.ConfigOverridesPreviewRequest{Category: "fragments", Full: true, Fragments: []protocol.OverrideFragment{{ID: "lab", Name: "Lab DNS", Enabled: true, YAML: "dns: {enable: true, nameserver: [1.1.1.1]}\nauthentication: ['alice:synthetic-preview-password']\nskip-auth-prefixes: ['10.0.0.0/8']"}}}
	got, err := api.PreviewConfigOverrides(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1.1.1.1", "Lab DNS", "alice:", "MATCH,DIRECT", `"target":"alice"`, `"target":"10.0.0.0/8"`} {
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
	if core.reloads != 0 {
		t.Fatalf("revealed preview reloaded core %d times", core.reloads)
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

func TestConfigOverridesPreview_ConcurrentClientsRetainTheirOpeningRevision(t *testing.T) {
	m, _, _ := newPagedOverridePreview(t, "Office")
	req := protocol.ConfigOverridesPreviewRequest{Category: "dns", Full: true}
	first, err := m.PreviewConfigOverrides(t.Context(), req)
	if err != nil || first.NextOffset == 0 {
		t.Fatalf("missing first page: %v", err)
	}
	// An unrelated status update can occur between two editors opening the same draft.
	updated := m.store.Load()
	updated.Revision++
	m.store.Store(updated)
	second, err := m.PreviewConfigOverrides(t.Context(), req)
	if err != nil || second.Revision != updated.Revision {
		t.Fatalf("missing second editor revision: %d %v", second.Revision, err)
	}
	req.Offset, req.Snapshot = first.NextOffset, first.Snapshot
	cont, err := m.PreviewConfigOverrides(t.Context(), req)
	if err != nil || cont.Revision != first.Revision {
		t.Fatalf("first editor's revision changed: got=%d want=%d err=%v", cont.Revision, first.Revision, err)
	}
}

func TestConfigOverridesPreview_NewManagerRejectsPreviousCursor(t *testing.T) {
	m, _, _ := newPagedOverridePreview(t, "Office")
	req := protocol.ConfigOverridesPreviewRequest{Category: "dns", Full: true}
	first, err := m.PreviewConfigOverrides(t.Context(), req)
	if err != nil || first.NextOffset == 0 {
		t.Fatalf("missing first page: %v", err)
	}
	replacement, _, _ := newPagedOverridePreview(t, "Office")
	req.Offset, req.Snapshot = first.NextOffset, first.Snapshot
	_, err = replacement.PreviewConfigOverrides(t.Context(), req)
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("restarted daemon accepted an old cursor: %v", err)
	}
}

func TestConfigOverridesPreview_PreservesSavedDataFailure(t *testing.T) {
	m := startupLoggingManager(t)
	m.settings.ControllerSecret = ""
	_, err := m.PreviewConfigOverrides(t.Context(), protocol.ConfigOverridesPreviewRequest{Category: "dns", Full: true})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || !strings.Contains(err.Error(), "controller secret") {
		t.Fatalf("reclassified settings failure: %v", err)
	}
}

type cancelDuringPreviewPage struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelDuringPreviewPage) Err() error {
	c.checks++
	if c.checks >= 2 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestConfigOverridesPreview_DeadlineInterruptsMultiSegmentLine(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &cancelDuringPreviewPage{Context: parent, cancel: cancel}
	_, err := newPreviewSnapshot(ctx, protocol.ConfigOverridesPreview{}, []protocol.ProxyAuthPreviewLine{{Text: strings.Repeat("a", 3*previewSegmentBytes)}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("single long line ignored cancellation: %v", err)
	}
}

func TestConfigOverridesPreview_CandidateCancellationPreservesClassification(t *testing.T) {
	m := startupLoggingManager(t)
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &cancelDuringPreviewPage{Context: parent, cancel: cancel, checks: 1}
	_, err := m.PreviewConfigOverrides(ctx, protocol.ConfigOverridesPreviewRequest{Category: "dns", Full: true})
	var api protocol.APIError
	if !errors.Is(err, context.Canceled) || errors.As(err, &api) {
		t.Fatalf("unexpected cancellation classification: %v", err)
	}
}

func TestConfigOverridesPreview_ExistingInvalidAuthenticationKeepsDataFailure(t *testing.T) {
	m := startupLoggingManager(t)
	m.settings.SetProxyAuth(protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "follow"})
	unchanged := m.settings.EffectiveProxyAuth()
	_, err := m.PreviewConfigOverrides(t.Context(), protocol.ConfigOverridesPreviewRequest{Category: "auth", Full: true, Authentication: &unchanged})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || !errors.Is(err, subscription.ErrProxyAuthNoUsers) {
		t.Fatalf("misclassified underlying authentication failure: %+v", err)
	}
}
