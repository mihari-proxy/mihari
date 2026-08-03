package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/LeeShunEE/mihari/internal/config"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/subscription"
)

type reloadController struct {
	fakeController
	mu        sync.Mutex
	reloads   int
	reloadErr error
}

func (c *reloadController) Reload(context.Context, string, bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reloads++
	return c.reloadErr
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
	b, err := manager.AddSubscription(context.Background(), Operation{ID: "add-b", Source: "test"}, AddSubscriptionInput{Name: "B", URL: url + "?name=b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RefreshSubscription(context.Background(), Operation{ID: "refresh-a", Source: "test"}, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RefreshSubscription(context.Background(), Operation{ID: "refresh-b", Source: "test"}, b.ID); err != nil {
		t.Fatal(err)
	}
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

func TestRefreshCannotRecreateSubscriptionDeletedDuringDownload(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	manager, _, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
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
	profile, _ := manager.AddSubscription(context.Background(), Operation{ID: "add", Source: "test"}, AddSubscriptionInput{Name: "A", URL: url})
	controller.reloadErr = protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "reload failed"}
	_, err := manager.RefreshSubscription(context.Background(), Operation{ID: "refresh", Source: "test"}, profile.ID)
	if err == nil {
		t.Fatal("expected reload failure")
	}
	got := manager.Subscriptions()
	if got.ActiveID != "" || got.Profiles[0].Generation != 0 {
		t.Fatalf("subscription state was not rolled back: %#v", got)
	}
}
