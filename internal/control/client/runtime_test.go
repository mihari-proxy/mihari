package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/coder/websocket"
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
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Method != test.method || request.URL.EscapedPath() != test.path || request.Header.Get("Authorization") != "Bearer token" {
					t.Errorf("request=%s %s auth=%q", request.Method, request.URL.EscapedPath(), request.Header.Get("Authorization"))
				}
				if test.body != "" {
					raw, _ := io.ReadAll(request.Body)
					if !sameJSON(raw, []byte(test.body)) {
						t.Errorf("body=%s want=%s", raw, test.body)
					}
				}
				_, _ = io.WriteString(response, test.response)
			}))
			defer server.Close()
			if err := test.invoke(context.Background(), NewHTTP(server.URL, "token", server.Client())); err != nil {
				t.Fatal(err)
			}
		})
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
