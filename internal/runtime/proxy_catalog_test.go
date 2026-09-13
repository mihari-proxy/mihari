package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/mihomo"
)

func TestProxyCatalog_FixedCandidateAndProjectionDeduplication(t *testing.T) {
	global := mihomo.Proxies{Proxies: map[string]mihomo.Proxy{"ordinary": {Name: "ordinary", Type: "Direct"}, "group": {Name: "group", All: []string{"ordinary", "duplicate", "duplicate"}}}}
	providers := mihomo.ProxyProviders{Providers: map[string]mihomo.ProxyProvider{
		"z":       {VehicleType: "HTTP", Proxies: []mihomo.Proxy{{Name: "duplicate", Type: "Trojan"}, {Name: "ordinary", Type: "Trojan"}}},
		"a":       {VehicleType: "File", Proxies: []mihomo.Proxy{{Name: "duplicate", Type: "Shadowsocks", UDP: true}, {Name: "unique", Type: "Trojan"}}},
		"default": {VehicleType: "Compatible", Proxies: []mihomo.Proxy{{Name: "ordinary"}, {Name: "group"}}},
	}}
	merged, duplicates, sources := resolveProxyCatalog(global, providers)
	if !reflect.DeepEqual(duplicates, []string{"duplicate", "ordinary"}) {
		t.Fatalf("duplicates=%v", duplicates)
	}
	if merged.Proxies["ordinary"].Type != "Direct" || merged.Proxies["duplicate"].Type != "Shadowsocks" || !merged.Proxies["duplicate"].UDP || sources["duplicate"] != "a" {
		t.Fatal("candidate identity changed")
	}
	if _, found := global.Proxies["duplicate"]; found {
		t.Fatal("mutated raw namespace")
	}
}

func TestProviderRead_RetryClassification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		calls int
	}{
		{"transient", protocol.APIError{Code: protocol.CodeUpstreamFailure, Details: map[string]any{"status": 503}}, 3},
		{"unauthorized", protocol.APIError{Code: protocol.CodePermissionDenied}, 1},
		{"unsupported", protocol.APIError{Code: protocol.CodeUpstreamFailure, Details: map[string]any{"status": 404}}, 1},
		{"decode", protocol.APIError{Code: protocol.CodeDataFailure}, 1},
		{"truncated success", diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Details: map[string]any{"status": 200}}, io.ErrUnexpectedEOF), 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, waits, warnings := 0, 0, 0
			_, err := readProxyProviders(context.Background(), func(ctx context.Context) (mihomo.ProxyProviders, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Error("unbounded attempt")
				}
				return mihomo.ProxyProviders{}, tc.err
			}, func(context.Context, time.Duration) error { waits++; return nil }, func(context.Context, diagnostics.Record) { warnings++ })
			if err == nil || calls != tc.calls || waits != calls-1 || warnings != calls-1 {
				t.Fatalf("calls=%d waits=%d warnings=%d err=%v", calls, waits, warnings, err)
			}
		})
	}
}

func TestProviderRead_RecoversAfterTransientFailure(t *testing.T) {
	calls := 0
	result, err := readProxyProviders(context.Background(), func(context.Context) (mihomo.ProxyProviders, error) {
		calls++
		if calls < 2 {
			return mihomo.ProxyProviders{}, io.ErrUnexpectedEOF
		}
		return mihomo.ProxyProviders{Providers: map[string]mihomo.ProxyProvider{"ready": {}}}, nil
	}, func(context.Context, time.Duration) error { return nil }, nil)
	if err != nil || calls != 2 || len(result.Providers) != 1 {
		t.Fatal("retry recovery failed")
	}
}

func TestProviderRead_CancellationStopsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	_, err := readProxyProviders(ctx, func(context.Context) (mihomo.ProxyProviders, error) {
		calls++
		return mihomo.ProxyProviders{}, io.ErrUnexpectedEOF
	}, func(context.Context, time.Duration) error { cancel(); return ctx.Err() }, nil)
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation did not stop retry")
	}
}

func TestProviderRead_RetryAfterExceedsBudget(t *testing.T) {
	calls := 0
	cause := &diagnostics.HTTPError{Status: 429, RetryDelay: time.Hour}
	rateLimited := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Details: map[string]any{"status": 429}}, cause)
	_, err := readProxyProviders(context.Background(), func(context.Context) (mihomo.ProxyProviders, error) {
		calls++
		return mihomo.ProxyProviders{}, rateLimited
	}, func(context.Context, time.Duration) error { t.Fatal("wait exceeds budget"); return nil }, nil)
	var api protocol.APIError
	if calls != 1 || !errors.Is(err, cause) || !errors.As(err, &api) || api.Details["status"] != 429 {
		t.Fatal("ignored Retry-After or lost terminal error")
	}
}

func TestDelayProxy_ProviderOnlyUsesScopedRoute(t *testing.T) {
	var path string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/proxies":
			_, _ = w.Write([]byte(`{"proxies":{"group":{"name":"group","type":"Selector","all":["Hong Kong 01"]}}}`))
		case "/providers/proxies":
			_, _ = w.Write([]byte(`{"providers":{"z":{"vehicleType":"HTTP","proxies":[{"name":"Hong Kong 01","type":"Trojan"}]},"a":{"vehicleType":"HTTP","proxies":[{"name":"Hong Kong 01","type":"Shadowsocks","udp":true}]}}}`))
		case "/providers/proxies/a/Hong Kong 01/healthcheck":
			path = r.URL.Path
			if r.URL.Query().Get("url") != "https://test.invalid/204" || r.URL.Query().Get("timeout") != "5000" {
				t.Error("lost delay parameters")
			}
			_ = json.NewEncoder(w).Encode(map[string]int{"delay": 42})
		default:
			http.Error(w, "proxy not found", 404)
		}
	}))
	defer upstream.Close()
	m := New(Options{Controller: mihomo.NewClient(upstream.URL, "", upstream.Client())})
	delay, err := m.DelayProxy(context.Background(), "Hong Kong 01", "https://test.invalid/204", 5000)
	if err != nil || delay != 42 || path == "" {
		t.Fatalf("provider delay=%d err=%v route=%q", delay, err, path)
	}
}
