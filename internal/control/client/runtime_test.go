package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestRuntimeClientFiniteEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		response string
		invoke   func(context.Context, *Client) error
	}{
		{"core", http.MethodGet, "/v1/core", "", `{"schema":"mihari/v1","revision":1,"status":"running","version":"v1"}`,
			func(ctx context.Context, client *Client) error { _, err := client.Core(ctx); return err }},
		{"install", http.MethodPost, "/v1/core/install", `{"operation_id":"op","if_revision":1}`, `{"schema":"mihari/v1","version":"v1","updated":true,"revision":2}`,
			func(ctx context.Context, client *Client) error {
				revision := uint64(1)
				_, err := client.InstallCore(ctx, protocol.MutationRequest{OperationID: "op", IfRevision: &revision})
				return err
			}},
		{"restart", http.MethodPost, "/v1/core/restart", `{"operation_id":"op"}`, `{"schema":"mihari/v1","operation_id":"op"}`,
			func(ctx context.Context, client *Client) error {
				_, err := client.RestartCore(ctx, protocol.MutationRequest{OperationID: "op"})
				return err
			}},
		{"proxies", http.MethodGet, "/v1/proxies", "", `{"schema":"mihari/v1","groups":[]}`,
			func(ctx context.Context, client *Client) error { _, err := client.ProxyGroups(ctx); return err }},
		{"select escaped", http.MethodPut, "/v1/proxy-groups/Auto%2FSelect", `{"operation_id":"op","name":"DIRECT"}`, `{"schema":"mihari/v1","operation_id":"op"}`,
			func(ctx context.Context, client *Client) error {
				_, err := client.SelectProxy(ctx, "Auto/Select", protocol.ProxySelectionRequest{OperationID: "op", Name: "DIRECT"})
				return err
			}},
		{"delay", http.MethodPost, "/v1/proxy-groups/GLOBAL/delay-test", `{"url":"https://example.com","timeout_ms":3000}`, `{"schema":"mihari/v1","delays":{"DIRECT":1}}`,
			func(ctx context.Context, client *Client) error {
				_, err := client.DelayTest(ctx, "GLOBAL", protocol.DelayTestRequest{URL: "https://example.com", TimeoutMilliseconds: 3000})
				return err
			}},
		{"delay proxy", http.MethodPost, "/v1/proxies/Node%20A/delay-test", `{"url":"https://example.com","timeout_ms":3000}`, `{"schema":"mihari/v1","delays":{"Node A":1}}`,
			func(ctx context.Context, client *Client) error {
				_, err := client.DelayProxy(ctx, "Node A", protocol.DelayTestRequest{URL: "https://example.com", TimeoutMilliseconds: 3000})
				return err
			}},
		{"connections", http.MethodGet, "/v1/connections", "", `{"schema":"mihari/v1","download_total":0,"upload_total":0,"connections":[]}`,
			func(ctx context.Context, client *Client) error { _, err := client.Connections(ctx); return err }},
		{"close", http.MethodDelete, "/v1/connections/id%2Fone", `{"operation_id":"op"}`, `{"schema":"mihari/v1","operation_id":"op"}`,
			func(ctx context.Context, client *Client) error {
				_, err := client.CloseConnection(ctx, "id/one", protocol.MutationRequest{OperationID: "op"})
				return err
			}},
		{"close all", http.MethodDelete, "/v1/connections", `{"operation_id":"op"}`, `{"schema":"mihari/v1","operation_id":"op"}`,
			func(ctx context.Context, client *Client) error {
				_, err := client.CloseAllConnections(ctx, protocol.MutationRequest{OperationID: "op"})
				return err
			}},
		{"rules", http.MethodGet, "/v1/rules", "", `{"schema":"mihari/v1","rules":[]}`,
			func(ctx context.Context, client *Client) error { _, err := client.Rules(ctx); return err }},
		{"rule providers", http.MethodGet, "/v1/rule-providers", "", `{"schema":"mihari/v1","providers":[]}`,
			func(ctx context.Context, client *Client) error { _, err := client.RuleProviders(ctx); return err }},
		{"update rule provider", http.MethodPost, "/v1/rule-providers/AI%2FSearch/update", `{"operation_id":"provider-1","if_revision":3}`, `{"schema":"mihari/v1","operation_id":"provider-1","revision":4}`,
			func(ctx context.Context, client *Client) error {
				revision := uint64(3)
				_, err := client.UpdateRuleProvider(ctx, "AI/Search", protocol.MutationRequest{OperationID: "provider-1", IfRevision: &revision})
				return err
			}},
		{"geoip status", http.MethodGet, "/v1/geoip/status", "", `{"schema":"mihari/v1","revision":1,"country":{"available":false},"asn":{"available":false}}`,
			func(ctx context.Context, client *Client) error { _, err := client.GeoIPStatus(ctx); return err }},
		{"geoip lookup", http.MethodPost, "/v1/geoip/lookup", `{"addresses":["1.1.1.1"]}`, `{"schema":"mihari/v1","records":[{"address":"1.1.1.1","country_code":"AU"}]}`,
			func(ctx context.Context, client *Client) error {
				_, err := client.LookupGeoIP(ctx, protocol.GeoIPLookupRequest{Addresses: []string{"1.1.1.1"}})
				return err
			}},
		{"geoip update", http.MethodPost, "/v1/geoip/update", `{"operation_id":"geoip-1","if_revision":1}`, `{"schema":"mihari/v1","operation_id":"geoip-1","revision":2,"status":{"schema":"mihari/v1","revision":2,"country":{"available":true},"asn":{"available":true}}}`,
			func(ctx context.Context, client *Client) error {
				revision := uint64(1)
				_, err := client.UpdateGeoIP(ctx, protocol.MutationRequest{OperationID: "geoip-1", IfRevision: &revision})
				return err
			}},
		{"logging", http.MethodGet, "/v1/logging", "", `{"schema":"mihari/v1","revision":7,"level":"debug","max_size_mb":20,"max_files":5,"dir":"C:/logs"}`,
			func(ctx context.Context, client *Client) error {
				_, err := client.Logging(ctx)
				return err
			}},
		{"update logging", http.MethodPatch, "/v1/logging", `{"operation_id":"logging-1","if_revision":7,"level":"debug","max_size_mb":20,"max_files":5}`, `{"schema":"mihari/v1","revision":8,"level":"debug","max_size_mb":20,"max_files":5,"dir":"C:/logs"}`,
			func(ctx context.Context, client *Client) error {
				revision := uint64(7)
				level := "debug"
				maxSizeMB := int64(20)
				maxFiles := int64(5)
				_, err := client.UpdateLogging(ctx, protocol.LoggingUpdateRequest{
					OperationID: "logging-1", IfRevision: &revision, Level: &level, MaxSizeMB: &maxSizeMB, MaxFiles: &maxFiles,
				})
				return err
			}},
		{"system proxy", http.MethodGet, "/v1/system-proxy", "", `{"schema":"mihari/v1","revision":5,"desired":true,"target":"127.0.0.1:9190","observed":{"enabled":true,"server":"127.0.0.1:9190","owned":true,"foreign":false}}`,
			func(ctx context.Context, client *Client) error { _, err := client.SystemProxy(ctx); return err }},
		{"system proxy enable", http.MethodPost, "/v1/system-proxy/enable", `{"operation_id":"sysproxy-1","if_revision":5,"force":true}`, `{"schema":"mihari/v1","revision":6,"desired":true,"target":"127.0.0.1:9190","observed":{"enabled":true,"server":"127.0.0.1:9190","owned":true,"foreign":false}}`,
			func(ctx context.Context, client *Client) error {
				revision := uint64(5)
				_, err := client.EnableSystemProxy(ctx, protocol.SystemProxyMutationRequest{OperationID: "sysproxy-1", IfRevision: &revision, Force: true})
				return err
			}},
		{"system proxy disable", http.MethodPost, "/v1/system-proxy/disable", `{"operation_id":"sysproxy-2"}`, `{"schema":"mihari/v1","revision":7,"desired":false,"observed":{"enabled":false,"owned":false,"foreign":false}}`,
			func(ctx context.Context, client *Client) error {
				_, err := client.DisableSystemProxy(ctx, protocol.SystemProxyMutationRequest{OperationID: "sysproxy-2"})
				return err
			}},
		{"tun", http.MethodGet, "/v1/tun", "", `{"schema":"mihari/v1","revision":5,"desired_enable":true,"live_enable":true,"stack":"gVisor","managed":true}`,
			func(ctx context.Context, client *Client) error { _, err := client.Tun(ctx); return err }},
		{"tun enable", http.MethodPost, "/v1/tun/enable", `{"operation_id":"tun-1","if_revision":5}`, `{"schema":"mihari/v1","revision":6,"desired_enable":true,"live_enable":true,"stack":"gVisor","managed":true}`,
			func(ctx context.Context, client *Client) error {
				revision := uint64(5)
				_, err := client.EnableTun(ctx, protocol.TunMutationRequest{OperationID: "tun-1", IfRevision: &revision})
				return err
			}},
		{"tun disable", http.MethodPost, "/v1/tun/disable", `{"operation_id":"tun-2"}`, `{"schema":"mihari/v1","revision":7,"desired_enable":false,"managed":true,"stack":"gVisor"}`,
			func(ctx context.Context, client *Client) error {
				_, err := client.DisableTun(ctx, protocol.TunMutationRequest{OperationID: "tun-2"})
				return err
			}},
		{"onboarding", http.MethodGet, "/v1/onboarding", "", `{"schema":"mihari/v1","revision":3,"complete":false,"mixed_addr":"127.0.0.1:7890","controller_addr":"127.0.0.1:9090","web_addr":"127.0.0.1:9191","restart_required":false}`,
			func(ctx context.Context, client *Client) error { _, err := client.Onboarding(ctx); return err }},
		{"update onboarding", http.MethodPatch, "/v1/onboarding", `{"operation_id":"setup-1","if_revision":3,"complete":true,"web_addr":"127.0.0.1:9292"}`, `{"schema":"mihari/v1","revision":4,"complete":true,"mixed_addr":"127.0.0.1:7890","controller_addr":"127.0.0.1:9090","web_addr":"127.0.0.1:9292","restart_required":true}`,
			func(ctx context.Context, client *Client) error {
				revision := uint64(3)
				complete := true
				webAddr := "127.0.0.1:9292"
				_, err := client.UpdateOnboarding(ctx, protocol.OnboardingUpdateRequest{OperationID: "setup-1", IfRevision: &revision, Complete: &complete, WebAddr: &webAddr})
				return err
			}},
		{"subscriptions", http.MethodGet, "/v1/subscriptions", "", `{"schema":"mihari/v1","revision":11,"active_id":"listed-one","global_interval":"9h","subscriptions":[{"id":"listed-one","name":"listed","enabled":true,"auto_refresh":false,"interval":"3h","cached":true,"generation":4,"proxy_mode":"auto"}]}`,
			func(ctx context.Context, client *Client) error {
				result, err := client.Subscriptions(ctx)
				if err != nil {
					return err
				}
				wantSubscription := protocol.Subscription{ID: "listed-one", Name: "listed", Enabled: true, AutoRefresh: false, Interval: "3h", Cached: true, Generation: 4, ProxyMode: "auto"}
				if result.Schema != "mihari/v1" || result.Revision != 11 || result.ActiveID != "listed-one" || result.GlobalInterval != "9h" || len(result.Subscriptions) != 1 || result.Subscriptions[0] != wantSubscription {
					return fmt.Errorf("subscription list=%#v", result)
				}
				return nil
			}},
		{"TUI preferences", http.MethodGet, "/v1/preferences/tui", "", `{"schema":"mihari/v1","revision":1,"connections_columns":["host","chain"]}`,
			func(ctx context.Context, client *Client) error { _, err := client.TUIPreferences(ctx); return err }},
		{"update TUI preferences", http.MethodPatch, "/v1/preferences/tui", `{"operation_id":"columns-1","if_revision":1,"connections_columns":["source","traffic"]}`, `{"schema":"mihari/v1","revision":2,"connections_columns":["source","traffic"]}`,
			func(ctx context.Context, client *Client) error {
				revision := uint64(1)
				_, err := client.UpdateTUIPreferences(ctx, protocol.UpdateTUIPreferencesRequest{
					OperationID: "columns-1", IfRevision: &revision, ConnectionsColumns: []string{"source", "traffic"},
				})
				return err
			}},
		{"subscription add", http.MethodPost, "/v1/subscriptions", `{"operation_id":"add-1","if_revision":11,"name":"added","url":"https://example.test/sub","proxy_mode":"proxy"}`, `{"schema":"mihari/v1","operation_id":"add-1","revision":12,"subscription":{"id":"added-one","name":"added","enabled":true,"auto_refresh":true,"interval":"6h","cached":false,"generation":0,"proxy_mode":"proxy"}}`,
			func(ctx context.Context, client *Client) error {
				revision := uint64(11)
				result, err := client.AddSubscription(ctx, protocol.SubscriptionAddRequest{OperationID: "add-1", IfRevision: &revision, Name: "added", URL: "https://example.test/sub", ProxyMode: "proxy"})
				if err != nil {
					return err
				}
				return assertSubscriptionResult(result, protocol.SubscriptionResult{
					Schema: "mihari/v1", OperationID: "add-1", Revision: 12,
					Subscription: protocol.Subscription{ID: "added-one", Name: "added", Enabled: true, AutoRefresh: true, Interval: "6h", Cached: false, Generation: 0, ProxyMode: "proxy"},
				})
			}},
		{"subscription refresh", http.MethodPost, "/v1/subscriptions/id%2Fone/refresh", `{"operation_id":"refresh-1","if_revision":12}`, `{"schema":"mihari/v1","operation_id":"refresh-1","revision":13,"subscription":{"id":"id/one","name":"refreshed","enabled":true,"auto_refresh":true,"interval":"2h","cached":true,"generation":8,"proxy_mode":"auto"}}`,
			func(ctx context.Context, client *Client) error {
				revision := uint64(12)
				result, err := client.RefreshSubscription(ctx, "id/one", protocol.MutationRequest{OperationID: "refresh-1", IfRevision: &revision})
				if err != nil {
					return err
				}
				return assertSubscriptionResult(result, protocol.SubscriptionResult{
					Schema: "mihari/v1", OperationID: "refresh-1", Revision: 13,
					Subscription: protocol.Subscription{ID: "id/one", Name: "refreshed", Enabled: true, AutoRefresh: true, Interval: "2h", Cached: true, Generation: 8, ProxyMode: "auto"},
				})
			}},
		{"subscription show", http.MethodGet, "/v1/subscriptions/id%2Fone", "", `{"schema":"mihari/v1","revision":14,"subscription":{"id":"id/one","name":"shown","enabled":true,"auto_refresh":false,"interval":"1h","cached":true,"generation":5}}`,
			func(ctx context.Context, client *Client) error {
				result, err := client.Subscription(ctx, "id/one")
				if err != nil {
					return err
				}
				return assertSubscriptionResult(result, protocol.SubscriptionResult{
					Schema: "mihari/v1", Revision: 14,
					Subscription: protocol.Subscription{ID: "id/one", Name: "shown", Enabled: true, AutoRefresh: false, Interval: "1h", Cached: true, Generation: 5},
				})
			}},
		{"subscription use", http.MethodPut, "/v1/subscriptions/id%2Fone/active", `{"operation_id":"use-1","if_revision":14}`, `{"schema":"mihari/v1","operation_id":"use-1","revision":15,"subscription":{"id":"id/one","name":"active","enabled":true,"auto_refresh":true,"interval":"4h","cached":true,"generation":6}}`,
			func(ctx context.Context, client *Client) error {
				revision := uint64(14)
				result, err := client.UseSubscription(ctx, "id/one", protocol.MutationRequest{OperationID: "use-1", IfRevision: &revision})
				if err != nil {
					return err
				}
				return assertSubscriptionResult(result, protocol.SubscriptionResult{
					Schema: "mihari/v1", OperationID: "use-1", Revision: 15,
					Subscription: protocol.Subscription{ID: "id/one", Name: "active", Enabled: true, AutoRefresh: true, Interval: "4h", Cached: true, Generation: 6},
				})
			}},
		{"subscription enable", http.MethodPut, "/v1/subscriptions/id%2Fone/enabled", `{"operation_id":"enable-1","if_revision":15,"enabled":false}`, `{"schema":"mihari/v1","operation_id":"enable-1","revision":16,"subscription":{"id":"id/one","name":"disabled","enabled":false,"auto_refresh":false,"interval":"5h","cached":true,"generation":6}}`,
			func(ctx context.Context, client *Client) error {
				revision := uint64(15)
				result, err := client.SetSubscriptionEnabled(ctx, "id/one", protocol.SubscriptionEnabledRequest{OperationID: "enable-1", IfRevision: &revision, Enabled: false})
				if err != nil {
					return err
				}
				return assertSubscriptionResult(result, protocol.SubscriptionResult{
					Schema: "mihari/v1", OperationID: "enable-1", Revision: 16,
					Subscription: protocol.Subscription{ID: "id/one", Name: "disabled", Enabled: false, AutoRefresh: false, Interval: "5h", Cached: true, Generation: 6},
				})
			}},
		{"subscription update", http.MethodPatch, "/v1/subscriptions/id%2Fone", `{"operation_id":"update-1","if_revision":16,"interval":"","auto_refresh":false}`, `{"schema":"mihari/v1","operation_id":"update-1","revision":17,"subscription":{"id":"id/one","name":"updated","enabled":true,"auto_refresh":false,"interval":"","cached":true,"generation":7,"proxy_mode":"proxy"}}`,
			func(ctx context.Context, client *Client) error {
				revision := uint64(16)
				interval := ""
				autoRefresh := false
				result, err := client.UpdateSubscription(ctx, "id/one", protocol.SubscriptionUpdateRequest{
					OperationID: "update-1", IfRevision: &revision, Interval: &interval, AutoRefresh: &autoRefresh,
				})
				if err != nil {
					return err
				}
				return assertSubscriptionResult(result, protocol.SubscriptionResult{
					Schema: "mihari/v1", OperationID: "update-1", Revision: 17,
					Subscription: protocol.Subscription{ID: "id/one", Name: "updated", Enabled: true, AutoRefresh: false, Interval: "", Cached: true, Generation: 7, ProxyMode: "proxy"},
				})
			}},
		{"subscription remove", http.MethodDelete, "/v1/subscriptions/id%2Fone", `{"operation_id":"remove-1","if_revision":17}`, `{"schema":"mihari/v1","operation_id":"remove-1","revision":18}`,
			func(ctx context.Context, client *Client) error {
				revision := uint64(17)
				result, err := client.RemoveSubscription(ctx, "id/one", protocol.MutationRequest{OperationID: "remove-1", IfRevision: &revision})
				if err != nil {
					return err
				}
				if result.Schema != "mihari/v1" || result.OperationID != "remove-1" || result.Revision != 18 {
					return fmt.Errorf("remove result=%#v", result)
				}
				return nil
			}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requestCount atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				requestCount.Add(1)
				if request.Method != test.method || request.URL.EscapedPath() != test.path || request.Header.Get("Authorization") != "Bearer token" {
					t.Errorf("request=%s %s auth=%q", request.Method, request.URL.EscapedPath(), request.Header.Get("Authorization"))
				}
				raw, readErr := io.ReadAll(request.Body)
				if readErr != nil {
					t.Errorf("read request body: %v", readErr)
				} else if test.body != "" {
					if request.Header.Get("Content-Type") != "application/json" {
						t.Errorf("content type=%q", request.Header.Get("Content-Type"))
					}
					if !sameJSON(raw, []byte(test.body)) {
						t.Errorf("body=%s want=%s", raw, test.body)
					}
				} else if len(raw) != 0 {
					t.Errorf("unexpected body=%s", raw)
				}
				_, _ = io.WriteString(response, test.response)
			}))
			defer server.Close()
			if err := test.invoke(context.Background(), NewHTTP(server.URL, "token", server.Client())); err != nil {
				t.Fatal(err)
			}
			if got := requestCount.Load(); got != 1 {
				t.Fatalf("request count=%d, want 1", got)
			}
		})
	}
}

func assertSubscriptionResult(result, want protocol.SubscriptionResult) error {
	if result != want {
		return fmt.Errorf("subscription result=%#v want=%#v", result, want)
	}
	return nil
}

func TestClientSetSubscriptionEnabledDecodesRevisionConflict(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestCount.Add(1)
		if request.Method != http.MethodPut || request.URL.EscapedPath() != "/v1/subscriptions/id%2Fone/enabled" || request.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request=%s %s auth=%q", request.Method, request.URL.EscapedPath(), request.Header.Get("Authorization"))
		}
		raw, readErr := io.ReadAll(request.Body)
		if readErr != nil || !sameJSON(raw, []byte(`{"operation_id":"enable-stale","if_revision":7,"enabled":false}`)) {
			t.Errorf("body=%s err=%v", raw, readErr)
		}
		response.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(response).Encode(protocol.NewError(protocol.CodeRevisionConflict, "subscription revision changed", map[string]any{
			"expected_revision": 7,
			"actual_revision":   8,
		}))
	}))
	defer server.Close()

	revision := uint64(7)
	_, err := NewHTTP(server.URL, "token", server.Client()).SetSubscriptionEnabled(context.Background(), "id/one", protocol.SubscriptionEnabledRequest{
		OperationID: "enable-stale",
		IfRevision:  &revision,
		Enabled:     false,
	})
	var apiErr protocol.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != protocol.CodeRevisionConflict || apiErr.Details["expected_revision"] != float64(7) || apiErr.Details["actual_revision"] != float64(8) {
		t.Fatalf("api error=%#v", apiErr)
	}
	if got := requestCount.Load(); got != 1 {
		t.Fatalf("request count=%d, want 1", got)
	}
}

func TestRuntimeClientStreamReadsStableEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/streams/logs" || request.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request=%s auth=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		event, _ := json.Marshal(protocol.StreamEvent{Schema: "mihari/v1", Stream: "logs", Data: json.RawMessage(`{"type":"info"}`)})
		_ = connection.Write(request.Context(), websocket.MessageText, event)
		_ = connection.Close(websocket.StatusNormalClosure, "done")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var events []protocol.StreamEvent
	err := NewHTTP(server.URL, "token", server.Client()).Stream(ctx, "logs", func(event protocol.StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Schema != "mihari/v1" || string(events[0].Data) != `{"type":"info"}` {
		t.Fatalf("events=%#v", events)
	}
}

func sameJSON(first, second []byte) bool {
	var a, b any
	return json.Unmarshal(first, &a) == nil && json.Unmarshal(second, &b) == nil && valuesEqual(a, b)
}

func valuesEqual(a, b any) bool {
	first, _ := json.Marshal(a)
	second, _ := json.Marshal(b)
	return string(first) == string(second)
}

func TestClientCoreDecodesLocalReadiness(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/core" || request.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request=%s auth=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(response, `{"schema":"mihari/v1","revision":1,"status":"running","localReady":true,"localVersion":"v1.18.5"}`)
	}))
	defer server.Close()
	status, err := NewHTTP(server.URL, "token", server.Client()).Core(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.LocalReady || status.LocalVersion != "v1.18.5" {
		t.Fatalf("status=%#v", status)
	}
}

func TestClientServiceStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/service/status" || request.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request=%s auth=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(response, `{"schema":"mihari/v1","status":"not_installed"}`)
	}))
	defer server.Close()
	status, err := NewHTTP(server.URL, "token", server.Client()).ServiceStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "not_installed" {
		t.Fatalf("status=%#v", status)
	}
}
