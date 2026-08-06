package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type reloadController struct {
	fakeController
	mu        sync.Mutex
	reloads   int
	reloadErr error
	reload    func(context.Context) error
}

func (c *reloadController) Reload(ctx context.Context, _ string, _ bool) error {
	c.mu.Lock()
	c.reloads++
	reload, reloadErr := c.reload, c.reloadErr
	c.mu.Unlock()
	if reload != nil {
		return reload(ctx)
	}
	return reloadErr
}

func subscriptionManager(t *testing.T, handler http.Handler) (*Manager, *subscription.Service, *reloadController, string) {
	t.Helper()
	root := t.TempDir()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	service, err := subscription.Open(subscription.ServiceOptions{
		CatalogPath: filepath.Join(root, "subscriptions", "catalog.yaml"),
		CacheDir:    filepath.Join(root, "subscriptions", "cache"),
		Downloader:  subscription.NewDownloader(server.Client()),
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	runtimeConfig := filepath.Join(root, "runtime", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(runtimeConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimeConfig, []byte("proxies: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	controller := &reloadController{}
	manager := newTestManager(Options{
		Subscriptions: service,
		Settings:      settings,
		RuntimeConfig: runtimeConfig,
		StagingDir:    filepath.Join(root, "staging"),
		ValidateConfig: func(context.Context, string) error {
			return nil
		},
		Controller: controller,
	})
	return manager, service, controller, server.URL
}

func TestSubscriptionRefreshAndOfflineSwitch(t *testing.T) {
	manager, _, controller, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		name := request.URL.Query().Get("name")
		_, _ = writer.Write([]byte("proxies:\n  - {name: " + name + "}\n"))
	}))
	a, err := manager.AddSubscription(context.Background(), Operation{ID: "add-a", Source: "test"}, AddSubscriptionInput{Name: "A", URL: url + "?name=a"})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Cached {
		t.Fatalf("add should fetch immediately: %#v", a)
	}
	b, err := manager.AddSubscription(context.Background(), Operation{ID: "add-b", Source: "test"}, AddSubscriptionInput{Name: "B", URL: url + "?name=b"})
	if err != nil {
		t.Fatal(err)
	}
	if !b.Cached {
		t.Fatalf("add should fetch immediately: %#v", b)
	}
	// First successful fetch becomes active; switch offline to B.
	if _, err := manager.UseSubscription(context.Background(), Operation{ID: "use-b", Source: "test"}, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := manager.Subscriptions().ActiveID; got != b.ID {
		t.Fatalf("active=%q", got)
	}
	controller.mu.Lock()
	reloads := controller.reloads
	controller.mu.Unlock()
	if reloads < 2 {
		t.Fatalf("reloads=%d", reloads)
	}
}

func TestAddSubscriptionFetchesImmediately(t *testing.T) {
	var fetches atomic.Int32
	manager, _, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		_, _ = writer.Write([]byte("proxies:\n  - {name: node-a, type: direct}\n"))
	}))
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "add", Source: "test"}, AddSubscriptionInput{Name: "Main", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	if fetches.Load() != 1 {
		t.Fatalf("fetches=%d want 1", fetches.Load())
	}
	if !profile.Cached || profile.Generation == 0 || profile.UpdatedAt.IsZero() {
		t.Fatalf("profile not cached after add: %#v", profile)
	}
	if got := manager.Subscriptions().ActiveID; got != profile.ID {
		t.Fatalf("first fetch should become active: %q", got)
	}
}

func TestAddSubscriptionKeepsProfileWhenFetchFails(t *testing.T) {
	manager, _, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "add", Source: "test"}, AddSubscriptionInput{Name: "Broken", URL: url})
	if err != nil {
		t.Fatalf("add should keep registration when fetch fails: %v", err)
	}
	if profile.Cached || profile.LastError == "" {
		t.Fatalf("expected uncached profile with last_error: %#v", profile)
	}
	if len(manager.Subscriptions().Profiles) != 1 {
		t.Fatalf("profiles=%#v", manager.Subscriptions().Profiles)
	}
}

func TestRefreshCannotRecreateSubscriptionDeletedDuringDownload(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	manager, _, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			// Auto-fetch on add completes immediately.
			_, _ = writer.Write([]byte("proxies: []\n"))
			return
		}
		close(entered)
		<-release
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "add", Source: "test"}, AddSubscriptionInput{Name: "A", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := manager.RefreshSubscription(context.Background(), Operation{ID: "refresh", Source: "test"}, profile.ID)
		done <- err
	}()
	<-entered
	if err := manager.RemoveSubscription(context.Background(), Operation{ID: "remove", Source: "test"}, profile.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	err = <-done
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("refresh error=%v", err)
	}
	if len(manager.Subscriptions().Profiles) != 0 {
		t.Fatal("deleted subscription was recreated")
	}
}

func TestReloadFailureRollsBackSubscriptionActivation(t *testing.T) {
	manager, _, controller, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	// Force the auto-fetch that runs inside Add to fail at mihomo reload.
	controller.reloadErr = protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "reload failed"}
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "add", Source: "test"}, AddSubscriptionInput{Name: "A", URL: url})
	if err != nil {
		t.Fatalf("add should not fail hard when auto-fetch reloads fail: %v", err)
	}
	got := manager.Subscriptions()
	if got.ActiveID != "" || got.Profiles[0].Generation != 0 || profile.Cached {
		t.Fatalf("failed auto-fetch should roll back cache/activation: profile=%#v catalog=%#v", profile, got)
	}
	if snapshot := manager.Snapshot(); snapshot.Health != "degraded" || snapshot.Config.DesiredRevision <= snapshot.Config.ObservedRevision {
		t.Fatalf("rollback failure was not published as degraded: %#v", snapshot)
	}
}

func TestProxySelectionWaitsForSubscriptionReload(t *testing.T) {
	manager, _, controller, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	// Add auto-fetches once; only block reload on the subsequent explicit refresh.
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "add", Source: "test"}, AddSubscriptionInput{Name: "A", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	reloadEntered := make(chan struct{})
	releaseReload := make(chan struct{})
	selected := make(chan struct{})
	controller.reload = func(context.Context) error {
		close(reloadEntered)
		<-releaseReload
		return nil
	}
	controller.fakeController.selectProxy = func(context.Context, string, string) error {
		close(selected)
		return nil
	}
	refreshDone := make(chan error, 1)
	go func() {
		_, err := manager.RefreshSubscription(context.Background(), Operation{ID: "refresh", Source: "test"}, profile.ID)
		refreshDone <- err
	}()
	<-reloadEntered
	selectDone := make(chan error, 1)
	go func() {
		selectDone <- manager.SelectProxy(context.Background(), Operation{ID: "select", Source: "test"}, "GLOBAL", "DIRECT")
	}()
	select {
	case <-selected:
		t.Fatal("proxy selection overlapped subscription reload")
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseReload)
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	if err := <-selectDone; err != nil {
		t.Fatal(err)
	}
}
