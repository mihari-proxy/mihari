//go:build linux || darwin

package integration_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	"github.com/mihari-proxy/mihari/internal/platform"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"go.yaml.in/yaml/v3"
)

func TestUnixProviderContract_PreservesIdentityAndCachesGenerationConflict(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("native ProviderStore requires the isolated root test environment tracked by T19")
	}
	ctx := context.Background()
	id := "0123456789abcdef0123456789abcdef"
	oldPayload := []byte("payload:\n  - old.example\n")
	newPayload := []byte("payload:\n  - new.example\n")
	started, release := make(chan struct{}), make(chan struct{})
	var downloads atomic.Int32
	providerSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if downloads.Add(1) == 2 {
			close(started)
			<-release
			_, _ = w.Write(newPayload)
			return
		}
		_, _ = w.Write(oldPayload)
	}))
	defer providerSource.Close()

	documentBytes := []byte("rule-providers:\n  domains: {type: http, behavior: domain, url: '" + providerSource.URL + "'}\nrules: ['RULE-SET,domains,DIRECT']\n")
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	input := subscription.PolicyInput{
		YAML: documentBytes, SubscriptionID: id, Generation: 7,
		CoreTag: "v1.19.30", OS: "linux", Arch: "amd64", Settings: settings,
	}
	requirements, err := subscription.NewRootConfigPolicy().Inspect(ctx, input)
	if err != nil || len(requirements.Providers) != 1 {
		t.Fatalf("Inspect: requirements=%#v err=%v", requirements, err)
	}
	complete := input
	complete.Resources = map[string][]byte{requirements.Providers[0].ResourceID: oldPayload}
	built, err := subscription.NewRootConfigPolicy().Build(ctx, complete)
	if err != nil || len(built.Providers) != 1 || built.Providers[0].SubscriptionID != id || built.Providers[0].Generation != 7 {
		t.Fatalf("Build: providers=%#v err=%v", built.Providers, err)
	}

	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := platform.OpenTrustedRoot(ctx, dataDir, platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	providerStore, err := subscription.NewProviderStore(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	downloader := subscription.NewDownloader(subscription.DownloaderOptions{Client: providerSource.Client()})
	resources := subscription.NewResourcePreparer(providerStore, downloader, nil)
	prepared, err := resources.Prepare(ctx, input, subscription.ProxyModeDirect, nil)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := prepared.Activate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, hash, _, _, err := activation.ValidationConfig(ctx)
	if err == nil {
		err = activation.PublishConfig(ctx, hash)
	}
	if err == nil {
		err = activation.Finish(ctx)
	}
	if closeErr := prepared.Close(ctx); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}

	catalogPath := filepath.Join(t.TempDir(), "subscriptions.yaml")
	cacheDir := filepath.Join(t.TempDir(), "cache")
	if err := subscription.Save(catalogPath, subscription.Catalog{
		Schema: subscription.CatalogSchema, GlobalInterval: "12h", ActiveID: id,
		Profiles: []subscription.Profile{{ID: id, Name: "active", URL: providerSource.URL, Enabled: true, Version: 1, Generation: 7}},
	}); err != nil {
		t.Fatal(err)
	}
	service, err := subscription.Open(subscription.ServiceOptions{CatalogPath: catalogPath, CacheDir: cacheDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(service.CachePath(id), documentBytes, 0600); err != nil {
		t.Fatal(err)
	}
	var reloads atomic.Int32
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.EscapedPath() == "/providers/rules/domains" {
			reloads.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer controller.Close()
	store := state.NewStore(state.Snapshot{Revision: 11})
	manager := runtimeapi.New(runtimeapi.Options{
		Store: store, Coordinator: state.NewCoordinator(store), Controller: mihomo.NewClient(controller.URL, "", controller.Client()),
		Subscriptions: service, Settings: settings, Resources: resources,
		RootConfigInput: func(_ context.Context, document subscription.Document, settings config.Settings) (subscription.PolicyInput, error) {
			raw, err := yaml.Marshal(document)
			return subscription.PolicyInput{YAML: raw, CoreTag: "v1.19.30", OS: "linux", Arch: "amd64", Settings: settings}, err
		},
	})
	revision := uint64(11)
	op := runtimeapi.Operation{ID: "op-1", Source: "cli", IfRevision: &revision}
	done := make(chan error, 1)
	go func() { done <- manager.RefreshProvider(ctx, op, "domains") }()
	<-started
	if _, _, err := service.Mutate(func(catalog *subscription.Catalog) error {
		catalog.Profiles[catalog.Index(id)].Generation = 8
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	first := <-done
	second := manager.RefreshProvider(ctx, op, "domains")
	var apiError protocol.APIError
	if !errors.As(first, &apiError) || apiError.Code != protocol.CodeRevisionConflict || second == nil || second.Error() != first.Error() {
		t.Fatalf("first=%v second=%v", first, second)
	}
	if downloads.Load() != 2 || reloads.Load() != 0 || manager.Snapshot().Revision != 11 {
		t.Fatalf("downloads=%d reloads=%d revision=%d", downloads.Load(), reloads.Load(), manager.Snapshot().Revision)
	}
}
