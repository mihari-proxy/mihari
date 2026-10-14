package integration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"github.com/mihari-proxy/mihari/internal/supervisor"
)

func TestSetupTargetIPC_ConcurrentObservationAndActivation(t *testing.T) {
	root := t.TempDir()
	entered, release := make(chan struct{}), make(chan struct{})
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write([]byte("proxies: []\n"))
	}))
	t.Cleanup(source.Close)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/configs" {
			t.Errorf("unexpected controller request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(controller.Close)
	subs, err := subscription.Open(subscription.ServiceOptions{CatalogPath: filepath.Join(root, "catalog.yaml"), CacheDir: filepath.Join(root, "cache"), Downloader: subscription.NewDownloader(subscription.DownloaderOptions{Client: source.Client()})})
	if err != nil {
		t.Fatal(err)
	}
	onboard, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(root, "onboarding.json"), InitialSetupRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "runtime.yaml")
	if err := os.WriteFile(runtimePath, []byte("proxies: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(state.Snapshot{Health: "ok"})
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	manager := runtimeapi.New(runtimeapi.Options{Store: store, Settings: settings, Subscriptions: subs, Onboarding: onboard, Controller: mihomo.NewClient(controller.URL, "fixture-secret", controller.Client()), RuntimeConfig: runtimePath, StagingDir: filepath.Join(root, "staging")})
	endpoint := transporttest.Endpoint(t)
	listener, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	s := server.New(server.Options{Token: "fixture-token", Store: store, Runtime: manager})
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("control server did not stop")
		}
	})
	client := controlclient.New(endpoint, "fixture-token")
	status, err := client.Status(t.Context())
	if err != nil || !slices.Contains(status.Capabilities, protocol.CapabilitySetupTarget) {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	result := make(chan error, 1)
	var aID string
	go func() {
		a, err := client.AddSubscription(ctx, protocol.SubscriptionAddRequest{OperationID: "setup-a", ExecutionMode: protocol.ExecutionModeSetup, Name: "A", URL: source.URL + "/a"})
		aID = a.Subscription.ID
		result <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	b, err := client.AddSubscription(t.Context(), protocol.SubscriptionAddRequest{OperationID: "other-b", Name: "B", URL: source.URL + "/b"})
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	manager.Observe(supervisor.Observation{Status: supervisor.StatusRunning, PID: 42})
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	list, err := client.Subscriptions(t.Context())
	if err != nil || len(list.Subscriptions) != 2 || (aID == "" || list.ActiveID != aID || list.ActiveID == b.Subscription.ID) {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if _, err := client.AddSubscription(t.Context(), protocol.SubscriptionAddRequest{OperationID: "setup-a", ExecutionMode: protocol.ExecutionModeSetup, Name: "A", URL: source.URL + "/a"}); err != nil {
		t.Fatal(err)
	}
	list, err = client.Subscriptions(t.Context())
	if err != nil || len(list.Subscriptions) != 2 {
		t.Fatalf("replay list=%+v err=%v", list, err)
	}
	_, err = client.AddSubscription(t.Context(), protocol.SubscriptionAddRequest{OperationID: "old-strict", IfRevision: &status.Revision, Name: "C", URL: source.URL + "/b"})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("ordinary global precondition lost: %v", err)
	}
}
