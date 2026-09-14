package integration

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	controlserver "github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	"github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

type providerHandlerTransport struct{ handler http.Handler }

func (t providerHandlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, r)
	return w.Result(), nil
}

func TestProviderDelay_ControlPlaneRoutesAndReportsOriginalFailure(t *testing.T) {
	var fail bool
	var routes []string
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routes = append(routes, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/proxies":
			_, _ = w.Write([]byte(`{"proxies":{"GLOBAL":{"name":"GLOBAL","all":["group"]},"group":{"name":"group","type":"Selector","all":["HK","HK","DIRECT"],"testUrl":"https://test.invalid/204"},"DIRECT":{"name":"DIRECT","type":"Direct"}}}`))
		case "/providers/proxies":
			if fail {
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"message":"provider temporarily unavailable","secret":"fixture-secret"}`))
				return
			}
			_, _ = w.Write([]byte(`{"providers":{"z":{"vehicleType":"HTTP","proxies":[{"name":"HK","type":"Trojan"}]},"a":{"vehicleType":"HTTP","proxies":[{"name":"HK","type":"Shadowsocks","udp":true}]}}}`))
		case "/providers/proxies/a/HK/healthcheck", "/proxies/DIRECT/delay":
			if r.URL.Query().Get("url") != "https://test.invalid/204" {
				t.Error("default URL changed")
			}
			_, _ = w.Write([]byte(`{"delay":42}`))
		default:
			http.Error(w, "unexpected route", 404)
		}
	})
	var logs bytes.Buffer
	redactor := logging.NewRedactor("fixture-secret")
	reporter := logging.NewDiagnosticReporter(slog.New(slog.NewTextHandler(&logs, nil)), redactor)
	store := state.NewStore(state.Snapshot{Revision: 7, ActiveSubscription: "provider-profile"})
	manager := runtime.New(runtime.Options{Store: store, Controller: mihomo.NewClient("http://core.invalid", "fixture-secret", &http.Client{Transport: providerHandlerTransport{upstream}}), DiagnosticReporter: reporter})
	server := controlserver.New(controlserver.Options{Token: "fixture-token", Store: store, Runtime: manager, DiagnosticReporter: reporter})
	client := controlclient.NewHTTP("http://control.invalid", "fixture-token", &http.Client{Transport: providerHandlerTransport{server.Handler()}})
	groups, err := client.ProxyGroups(context.Background())
	if err != nil || len(groups.DuplicateNames) != 1 || groups.DuplicateNames[0] != "HK" || groups.Groups[0].Nodes[0].Type != "Shadowsocks" || !groups.Groups[0].Nodes[0].UDP {
		t.Fatalf("incomplete catalog: %#v err=%v", groups, err)
	}
	if groups.Revision == nil || *groups.Revision != 7 || groups.SubscriptionID != "provider-profile" {
		t.Fatal("provider catalog lost routing snapshot identity")
	}
	for _, name := range []string{"HK", "DIRECT"} {
		result, err := client.DelayProxy(context.Background(), name, protocol.DelayTestRequest{})
		if err != nil || result.Delays[name] != 42 {
			t.Fatalf("delay %s failed: %v", name, err)
		}
	}
	for _, path := range routes {
		if path == "/proxies/HK/delay" {
			t.Fatal("used ordinary route for provider-only node")
		}
	}
	fail = true
	_, err = client.ProxyGroups(context.Background())
	var api protocol.APIError
	if !errors.As(err, &api) || api.Details["status"] != float64(503) || strings.Contains(err.Error(), "temporarily") || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("unsafe failure envelope: %v", err)
	}
	if !strings.Contains(logs.String(), "provider temporarily unavailable") || !strings.Contains(logs.String(), "fixture-secret") {
		t.Fatal("raw diagnostics lost original HTTP body")
	}
	if strings.Count(logs.String(), "proxy_provider.retry") != 2 || strings.Count(logs.String(), "request_failed") != 1 {
		t.Fatal("retry/final diagnostic ownership changed")
	}
}
