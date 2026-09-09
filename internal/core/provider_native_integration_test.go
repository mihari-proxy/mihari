package core_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/core"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

func TestRootManager_NativeProviderDefinitionsReachTrustedExecutorUnchanged(t *testing.T) {
	var executorFetching atomic.Bool
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !executorFetching.Load() {
			t.Error("Mihari downloaded native provider outside the executor fixture")
		}
		requests.Add(1)
		if r.URL.Path != "/native-provider" {
			t.Error("provider path rewritten")
		}
		_, _ = io.WriteString(w, "payload: [example.test]\n")
	}))
	defer provider.Close()
	raw := []byte(fmt.Sprintf(`rule-providers:
  native:
    type: http
    behavior: domain
    format: yaml
    url: %s/native-provider
    path: ./native-provider.yaml
    interval: 123
    x-native: {nested: [preserved, extension]}
proxy-providers:
  local:
    type: file
    path: ./original/proxies.yaml
    x-native: {other: {enabled: true}}
rules: ['RULE-SET,native,DIRECT']
tun: {enable: true, stack: gvisor, x-native: [preserved]}
`, provider.URL))
	var service *subscription.Service
	var settings config.Settings
	m, f, c, _ := seamManager(t, func(o *runtimeapi.Options) {
		var err error
		service, err = subscription.Open(subscription.ServiceOptions{CatalogPath: filepath.Join(t.TempDir(), "catalog.yaml"), CacheDir: t.TempDir(), Downloader: seamFetcher{content: raw}})
		if err != nil {
			t.Fatal(err)
		}
		o.Subscriptions = service
		settings = o.Settings
	})
	document, err := subscription.ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	// This is the same Generate path used without the trusted Unix capability.
	expected, err := subscription.Generate(document, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	validations := 0
	f.Execute = func(ctx context.Context, command core.CoreCommand) ([]byte, error) {
		if command.Args[0] != "-t" {
			return []byte("Mihomo v1.19.30"), nil
		}
		validations++
		if !bytes.Equal(f.CommandConfig(command), expected) {
			t.Error("trusted and ordinary generation differ")
		}
		executorFetching.Store(true)
		defer executorFetching.Store(false)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.URL+"/native-provider", nil)
		if err != nil {
			return nil, err
		}
		response, err := provider.Client().Do(request)
		if err != nil {
			return nil, err
		}
		content, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if string(content) != "payload: [example.test]\n" {
			t.Error("unexpected native provider payload")
		}
		return []byte("Mihomo v1.19.30"), nil
	}
	p, err := service.Add("native", "https://example.invalid/subscription", subscription.ProxyModeDirect)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.RefreshSubscription(context.Background(), runtimeapi.Operation{ID: "native-cache", Source: "test"}, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = m.UseSubscription(context.Background(), runtimeapi.Operation{ID: "native-use", Source: "test"}, p.ID); err != nil {
		t.Fatal(err)
	}
	if validations != int(requests.Load()) || validations == 0 {
		t.Fatal("provider work did not belong solely to executor")
	}
	if !bytes.Equal(f.Content(), expected) || c.reloads != 2 {
		t.Fatalf("accepted native config mismatch=%v reloads=%d", !bytes.Equal(f.Content(), expected), c.reloads)
	}
	cached, _, err := service.ReadCache(p.ID)
	if err != nil || !bytes.Equal(cached, raw) {
		t.Fatal("original cache rewritten", err)
	}
}
