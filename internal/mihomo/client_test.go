package mihomo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

func TestClientVersionUsesBearerAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/version" {
			t.Fatalf("request=%s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer controller-secret" {
			t.Fatalf("authorization=%q", got)
		}
		_, _ = io.WriteString(response, `{"meta":true,"version":"v1.19.0"}`)
	}))
	defer server.Close()

	version, err := NewClient(server.URL, "controller-secret", server.Client()).Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !version.Meta || version.Version != "v1.19.0" {
		t.Fatalf("version=%#v", version)
	}
}

func TestClientRuntimeRequests(t *testing.T) {
	tests := []struct {
		name       string
		invoke     func(context.Context, *Client) error
		method     string
		path       string
		query      url.Values
		body       string
		response   string
		statusCode int
	}{
		{
			name: "proxies", method: http.MethodGet, path: "/proxies", response: `{"proxies":{"GLOBAL":{"name":"GLOBAL","type":"Selector","now":"node-a","all":["node-a"]},"node-a":{"name":"node-a","type":"VLESS","udp":true,"xudp":true}}}`,
			invoke: func(ctx context.Context, client *Client) error {
				got, err := client.Proxies(ctx)
				if err == nil && (got.Proxies["GLOBAL"].Now != "node-a" || !got.Proxies["node-a"].UDP || !got.Proxies["node-a"].XUDP) {
					t.Fatalf("proxies=%#v", got)
				}
				return err
			},
		},
		{
			name: "select escaped group", method: http.MethodPut, path: "/proxies/Auto%2FSelect", body: `{"name":"Node A"}`,
			invoke: func(ctx context.Context, client *Client) error {
				return client.SelectProxy(ctx, "Auto/Select", "Node A")
			},
		},
		{
			name: "delay group", method: http.MethodGet, path: "/group/Auto%2FSelect/delay", query: url.Values{"url": {"https://example.com/ping"}, "timeout": {"3500"}}, response: `{"Node A":42}`,
			invoke: func(ctx context.Context, client *Client) error {
				got, err := client.DelayGroup(ctx, "Auto/Select", "https://example.com/ping", 3500)
				if err == nil && got["Node A"] != 42 {
					t.Fatalf("delays=%#v", got)
				}
				return err
			},
		},
		{
			name: "delay proxy", method: http.MethodGet, path: "/proxies/Node%20A/delay", query: url.Values{"url": {"https://example.com/ping"}, "timeout": {"3500"}}, response: `{"delay":42}`,
			invoke: func(ctx context.Context, client *Client) error {
				delay, err := client.DelayProxy(ctx, "Node A", "https://example.com/ping", 3500)
				if err == nil && delay != 42 {
					t.Fatalf("delay=%d", delay)
				}
				return err
			},
		},
		{
			name: "connections", method: http.MethodGet, path: "/connections", response: `{"downloadTotal":2,"uploadTotal":3,"connections":[{"id":"abc","upload":3,"download":2}]}`,
			invoke: func(ctx context.Context, client *Client) error {
				got, err := client.Connections(ctx)
				if err == nil && (len(got.Connections) != 1 || got.Connections[0].ID != "abc") {
					t.Fatalf("connections=%#v", got)
				}
				return err
			},
		},
		{
			name: "close connection", method: http.MethodDelete, path: "/connections/id%2Fwith%2Fslashes",
			invoke: func(ctx context.Context, client *Client) error { return client.CloseConnection(ctx, "id/with/slashes") },
		},
		{
			name: "close all", method: http.MethodDelete, path: "/connections",
			invoke: func(ctx context.Context, client *Client) error { return client.CloseAllConnections(ctx) },
		},
		{
			name: "rules", method: http.MethodGet, path: "/rules", response: `{"rules":[{"type":"DOMAIN","payload":"example.com","proxy":"DIRECT"}]}`,
			invoke: func(ctx context.Context, client *Client) error {
				got, err := client.Rules(ctx)
				if err == nil && (len(got.Rules) != 1 || got.Rules[0].Proxy != "DIRECT") {
					t.Fatalf("rules=%#v", got)
				}
				return err
			},
		},
		{
			name: "rule providers", method: http.MethodGet, path: "/providers/rules", response: `{"providers":{"OpenAI":{"behavior":"Classical","format":"YamlRule","name":"OpenAI","ruleCount":12,"type":"Rule","vehicleType":"HTTP","updatedAt":"2026-08-03T01:02:03Z"}}}`,
			invoke: func(ctx context.Context, client *Client) error {
				got, err := client.RuleProviders(ctx)
				provider := got.Providers["OpenAI"]
				if err == nil && (provider.Name != "OpenAI" || provider.VehicleType != "HTTP" || provider.RuleCount != 12 || provider.UpdatedAt.IsZero()) {
					t.Fatalf("providers=%#v", got)
				}
				return err
			},
		},
		{
			name: "update escaped rule provider", method: http.MethodPut, path: "/providers/rules/AI%2FSearch", statusCode: http.StatusNoContent,
			invoke: func(ctx context.Context, client *Client) error {
				return client.UpdateRuleProvider(ctx, "AI/Search")
			},
		},
		{
			name: "reload", method: http.MethodPut, path: "/configs", query: url.Values{"force": {"true"}}, body: `{"path":"C:\\managed\\config.yaml"}`,
			invoke: func(ctx context.Context, client *Client) error {
				return client.Reload(ctx, `C:\managed\config.yaml`, true)
			},
		},
		{
			name: "restart", method: http.MethodPost, path: "/restart",
			invoke: func(ctx context.Context, client *Client) error { return client.Restart(ctx) },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Method != test.method || request.URL.EscapedPath() != test.path {
					t.Errorf("request=%s %s", request.Method, request.URL.EscapedPath())
				}
				if test.query != nil && request.URL.Query().Encode() != test.query.Encode() {
					t.Errorf("query=%q want=%q", request.URL.RawQuery, test.query.Encode())
				}
				if test.body != "" {
					var got, want any
					if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
						t.Error(err)
					}
					if err := json.Unmarshal([]byte(test.body), &want); err != nil {
						t.Fatal(err)
					}
					gotJSON, _ := json.Marshal(got)
					wantJSON, _ := json.Marshal(want)
					if string(gotJSON) != string(wantJSON) {
						t.Errorf("body=%s want=%s", gotJSON, wantJSON)
					}
				}
				status := test.statusCode
				if status == 0 {
					status = http.StatusOK
				}
				response.WriteHeader(status)
				_, _ = io.WriteString(response, test.response)
			}))
			defer server.Close()
			if err := test.invoke(context.Background(), NewClient(server.URL, "secret", server.Client())); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUpdateRuleProviderDoesNotLeakControllerOrUpstreamBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(response, `update https://user:password@example.test/rules?token=secret failed`)
	}))
	defer server.Close()

	err := NewClient(server.URL, "controller-secret", server.Client()).UpdateRuleProvider(context.Background(), "private")
	message := err.Error()
	for _, sensitive := range []string{"controller-secret", "password", "token=secret", "example.test"} {
		if strings.Contains(message, sensitive) {
			t.Fatalf("error leaked %q: %s", sensitive, message)
		}
	}
}

func TestClientClassifiesErrorsAndBoundsResponses(t *testing.T) {
	tests := []struct {
		name string
		code int
		body string
		want protocol.ErrorCode
	}{
		{"unauthorized", http.StatusUnauthorized, `no`, protocol.CodePermissionDenied},
		{"upstream failure", http.StatusInternalServerError, `failed`, protocol.CodeUpstreamFailure},
		{"invalid JSON", http.StatusOK, `{`, protocol.CodeDataFailure},
		{"oversized", http.StatusOK, strings.Repeat("x", maxResponseSize+1), protocol.CodeDataFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(test.code)
				_, _ = io.WriteString(response, test.body)
			}))
			defer server.Close()
			_, err := NewClient(server.URL, "secret", server.Client()).Version(context.Background())
			var apiError protocol.APIError
			if !errors.As(err, &apiError) || apiError.Code != test.want {
				t.Fatalf("err=%v want=%s", err, test.want)
			}
		})
	}
}
