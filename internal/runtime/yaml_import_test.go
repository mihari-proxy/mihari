package runtime

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

func TestAddSubscription_FailedInitialFetchDoesNotCreate(t *testing.T) {
	m, s, _, address := subscriptionManager(t, http.NotFoundHandler())
	if _, err := m.AddSubscription(context.Background(), Operation{ID: "missing-initial", Source: "test"}, AddSubscriptionInput{Name: "Missing", URL: address}); err == nil {
		t.Fatal("failed initial fetch returned success")
	}
	if got := s.Snapshot(); len(got.Profiles) != 0 || got.ActiveID != "" {
		t.Fatalf("failed add persisted: %#v", got)
	}
}

func TestAddSubscription_FileReferencesRequireAcknowledgement(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "remote", true: "local"}[local], func(t *testing.T) {
			content := []byte("proxies: []\nproxy-providers:\n  local: {type: file, path: nodes.yaml}\n")
			m, s, _, source := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(content) }))
			if local {
				path := filepath.Join(t.TempDir(), "main.yaml")
				if err := os.WriteFile(path, content, 0600); err != nil {
					t.Fatal(err)
				}
				source, _ = platform.FileURI(path)
			}
			input := AddSubscriptionInput{Name: "refs", URL: source}
			_, err := m.AddSubscription(context.Background(), Operation{ID: "no-ack"}, input)
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodeInvalidArgument || api.Details["confirmation_required"] != "file_references" {
				t.Fatalf("err=%v", err)
			}
			if len(s.Snapshot().Profiles) != 0 {
				t.Fatal("confirmation refusal saved profile")
			}
			input.AllowFileReferences = true
			p, err := m.AddSubscription(context.Background(), Operation{ID: "ack"}, input)
			if err != nil || !p.Cached {
				t.Fatalf("profile=%+v err=%v", p, err)
			}
			// Refresh never asks again, including for an unchanged file reference.
			if _, err := m.RefreshSubscription(context.Background(), Operation{ID: "refresh"}, p.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLocalSource_EditRetainsCacheDirectoryAndRejectsTypeChange(t *testing.T) {
	m, s, _, remote := subscriptionManager(t, http.NotFoundHandler())
	dir := t.TempDir()
	path := filepath.Join(dir, "main.yaml")
	if err := os.WriteFile(path, []byte("proxies: []\nproxy-providers:\n  local: {type: file, path: nodes.yaml}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	uri, _ := platform.FileURI(path)
	p, err := m.AddSubscription(context.Background(), Operation{ID: "add-local"}, AddSubscriptionInput{Name: "local", URL: uri, AllowFileReferences: true})
	if err != nil {
		t.Fatal(err)
	}
	newURI, _ := platform.FileURI(filepath.Join(t.TempDir(), "missing.yaml"))
	if _, err := m.SetSubscription(context.Background(), Operation{ID: "edit"}, p.ID, SetSubscriptionInput{URL: &newURI}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetSubscription(context.Background(), Operation{ID: "type"}, p.ID, SetSubscriptionInput{URL: &remote}); err == nil {
		t.Fatal("allowed source type conversion")
	}
	if _, err := m.RefreshSubscription(context.Background(), Operation{ID: "missing"}, p.ID); err == nil {
		t.Fatal("missing source refresh succeeded")
	}
	_, doc, err := s.ReadCache(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	provider := doc["proxy-providers"].(subscription.Document)["local"].(subscription.Document)
	if provider["path"] != filepath.Join(dir, "nodes.yaml") {
		t.Fatalf("path=%v", provider["path"])
	}
	if !m.Subscriptions().Profiles[0].CacheOutdated {
		t.Fatal("source change not marked outdated")
	}
	runtime, err := os.ReadFile(m.runtimeConfig)
	if err != nil || !strings.Contains(string(runtime), "nodes.yaml") {
		t.Fatalf("runtime=%s err=%v", runtime, err)
	}
}

func TestAddSubscription_ValidationFailureDoesNotSave(t *testing.T) {
	m, s, _, source := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	cause := errors.New("invalid candidate")
	m.validateConfig = func(context.Context, string) error { return cause }
	_, err := m.AddSubscription(context.Background(), Operation{ID: "invalid"}, AddSubscriptionInput{Name: "invalid", URL: source})
	if !errors.Is(err, cause) || len(s.Snapshot().Profiles) != 0 {
		t.Fatalf("err=%v catalog=%+v", err, s.Snapshot())
	}
}
