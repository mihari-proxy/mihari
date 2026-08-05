package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
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
