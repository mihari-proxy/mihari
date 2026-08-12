package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestClientWebGUIRoundTrip(t *testing.T) {
	var sawInstallPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/web-gui":
			_ = json.NewEncoder(w).Encode(protocol.WebGUIStatus{
				Schema: "mihari/v1", GatewayAddr: "127.0.0.1:9191", GatewayHealth: "healthy",
				Safeguards: protocol.GatewaySafeguards{LoopbackBound: true, BrowserAuthenticated: true, ControllerIsolated: true, MutationsCoordinated: true},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/panels":
			_ = json.NewEncoder(w).Encode(protocol.PanelList{
				Schema: "mihari/v1", Panels: []protocol.PanelStatus{{ID: "zashboard", Name: "Zashboard", Health: "missing"}},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/install"):
			sawInstallPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(protocol.MutationResult{Schema: "mihari/v1", OperationID: "op-1", Revision: 2})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/web-gui/open":
			var body protocol.WebGUIOpenRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			openURL := "http://127.0.0.1:9191/__mihari/panels/zashboard/?token=abc"
			if body.Panel != "" {
				openURL = "http://127.0.0.1:9191/__mihari/panels/" + body.Panel + "/?token=abc"
			}
			_ = json.NewEncoder(w).Encode(protocol.WebGUIOpenResult{Schema: "mihari/v1", OpenURL: openURL, Panel: body.Panel})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := NewHTTP(server.URL, "tok", server.Client())
	status, err := c.WebGUI(context.Background())
	if err != nil || status.GatewayAddr != "127.0.0.1:9191" {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	// Status DTO from client must remain free of open_url field content from status endpoint.
	raw, _ := json.Marshal(status)
	if strings.Contains(strings.ToLower(string(raw)), "token") || strings.Contains(string(raw), "open_url") {
		t.Fatalf("status leaked secrets: %s", raw)
	}

	list, err := c.Panels(context.Background())
	if err != nil || len(list.Panels) != 1 {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	result, err := c.InstallPanel(context.Background(), "zashboard", protocol.PanelInstallRequest{OperationID: "op-1"})
	if err != nil || result.Revision != 2 || sawInstallPath != "/v1/panels/zashboard/install" {
		t.Fatalf("install=%#v path=%s err=%v", result, sawInstallPath, err)
	}
	open, err := c.OpenWebGUI(context.Background(), "")
	if err != nil || !strings.Contains(open.OpenURL, "token=") {
		t.Fatalf("open=%#v err=%v", open, err)
	}
	open, err = c.OpenWebGUI(context.Background(), "metacubexd")
	if err != nil || open.Panel != "metacubexd" || !strings.Contains(open.OpenURL, "/__mihari/panels/metacubexd/") {
		t.Fatalf("open selected=%#v err=%v", open, err)
	}
}

func TestClientPanelMutationContracts(t *testing.T) {
	revision := uint64(5)
	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		operationID string
		invoke      func(context.Context, *Client) (protocol.MutationResult, error)
	}{
		{
			name:        "install",
			method:      http.MethodPost,
			path:        "/v1/panels/id%2Fone/install",
			body:        `{"operation_id":"install-1","if_revision":5,"build":"v1.2.3"}`,
			operationID: "install-1",
			invoke: func(ctx context.Context, client *Client) (protocol.MutationResult, error) {
				return client.InstallPanel(ctx, "id/one", protocol.PanelInstallRequest{OperationID: "install-1", IfRevision: &revision, Build: "v1.2.3"})
			},
		},
		{
			name:        "update",
			method:      http.MethodPost,
			path:        "/v1/panels/id%2Fone/update",
			body:        `{"operation_id":"update-1","if_revision":5}`,
			operationID: "update-1",
			invoke: func(ctx context.Context, client *Client) (protocol.MutationResult, error) {
				return client.UpdatePanel(ctx, "id/one", protocol.MutationRequest{OperationID: "update-1", IfRevision: &revision})
			},
		},
		{
			name:        "activate",
			method:      http.MethodPut,
			path:        "/v1/panels/id%2Fone/active",
			body:        `{"operation_id":"activate-1","if_revision":5}`,
			operationID: "activate-1",
			invoke: func(ctx context.Context, client *Client) (protocol.MutationResult, error) {
				return client.ActivatePanel(ctx, "id/one", protocol.MutationRequest{OperationID: "activate-1", IfRevision: &revision})
			},
		},
		{
			name:        "rollback",
			method:      http.MethodPost,
			path:        "/v1/panels/id%2Fone/rollback",
			body:        `{"operation_id":"rollback-1","if_revision":5}`,
			operationID: "rollback-1",
			invoke: func(ctx context.Context, client *Client) (protocol.MutationResult, error) {
				return client.RollbackPanel(ctx, "id/one", protocol.MutationRequest{OperationID: "rollback-1", IfRevision: &revision})
			},
		},
		{
			name:        "uninstall",
			method:      http.MethodPost,
			path:        "/v1/panels/id%2Fone/uninstall",
			body:        `{"operation_id":"uninstall-1","if_revision":5}`,
			operationID: "uninstall-1",
			invoke: func(ctx context.Context, client *Client) (protocol.MutationResult, error) {
				return client.UninstallPanel(ctx, "id/one", protocol.MutationRequest{OperationID: "uninstall-1", IfRevision: &revision})
			},
		},
		{
			name:        "reinstall",
			method:      http.MethodPost,
			path:        "/v1/panels/id%2Fone/reinstall",
			body:        `{"operation_id":"reinstall-1","if_revision":5}`,
			operationID: "reinstall-1",
			invoke: func(ctx context.Context, client *Client) (protocol.MutationResult, error) {
				return client.ReinstallPanel(ctx, "id/one", protocol.MutationRequest{OperationID: "reinstall-1", IfRevision: &revision})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Method != test.method || request.URL.EscapedPath() != test.path || request.Header.Get("Authorization") != "Bearer tok" {
					t.Errorf("request=%s %s auth=%q", request.Method, request.URL.EscapedPath(), request.Header.Get("Authorization"))
				}
				var body any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Errorf("decode request body: %v", err)
					return
				}
				if raw, err := json.Marshal(body); err != nil || !sameJSON(raw, []byte(test.body)) {
					t.Errorf("body=%s want=%s", raw, test.body)
				}
				_ = json.NewEncoder(response).Encode(protocol.MutationResult{Schema: "mihari/v1", OperationID: test.operationID, Revision: 6})
			}))

			result, err := test.invoke(context.Background(), NewHTTP(server.URL, "tok", server.Client()))
			server.Close()
			if err != nil || result.Schema != "mihari/v1" || result.OperationID != test.operationID || result.Revision != 6 {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestClientWebGUIOpenRequiresAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(protocol.NewError(protocol.CodePermissionDenied, "control authentication failed", nil))
	}))
	defer server.Close()
	c := NewHTTP(server.URL, "bad", server.Client())
	if _, err := c.WebGUI(context.Background()); err == nil {
		t.Fatal("expected auth failure")
	}
}
