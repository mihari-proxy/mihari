package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/panel"
	runtimeapi "github.com/LeeShunEE/mihari/internal/runtime"
	"github.com/LeeShunEE/mihari/internal/state"
)

type panelFakeRuntime struct {
	*fakeRuntime
	webGUI  protocol.WebGUIStatus
	panels  []panel.PanelInfo
	openURL string
	lastOp  runtimeapi.Operation
	lastID  string
	lastPin string
	lastAct string
}

func (f *panelFakeRuntime) WebGUIStatus(context.Context) (protocol.WebGUIStatus, error) {
	return f.webGUI, nil
}
func (f *panelFakeRuntime) ListPanels(context.Context) ([]panel.PanelInfo, error) {
	return append([]panel.PanelInfo(nil), f.panels...), nil
}
func (f *panelFakeRuntime) InstallPanel(_ context.Context, op runtimeapi.Operation, id, pin string) error {
	f.lastOp, f.lastID, f.lastPin = op, id, pin
	f.snapshot.Revision++
	return nil
}
func (f *panelFakeRuntime) UpdatePanel(_ context.Context, op runtimeapi.Operation, id string) error {
	f.lastOp, f.lastID = op, id
	f.snapshot.Revision++
	return nil
}
func (f *panelFakeRuntime) ActivatePanel(_ context.Context, op runtimeapi.Operation, id string) error {
	f.lastOp, f.lastAct = op, id
	f.snapshot.Revision++
	return nil
}
func (f *panelFakeRuntime) RollbackPanel(_ context.Context, op runtimeapi.Operation, id string) error {
	f.lastOp, f.lastID = op, id
	f.snapshot.Revision++
	return nil
}
func (f *panelFakeRuntime) OpenWebGUI(_ context.Context, panelID string) (string, string, error) {
	return f.openURL, panelID, nil
}

func TestWebGUIAndPanelRoutesSecretFreeAndMutate(t *testing.T) {
	fake := &panelFakeRuntime{
		fakeRuntime: &fakeRuntime{snapshot: state.Snapshot{Revision: 3}},
		webGUI: protocol.WebGUIStatus{
			Schema: "mihari/v1", Revision: 3, GatewayAddr: "127.0.0.1:9191", GatewayHealth: "healthy",
			ActivePanel: "zashboard", BrowserSessions: 1,
			Panels:     []protocol.PanelStatus{{ID: "zashboard", Name: "Zashboard", Active: true, InstalledBuild: "v1", Health: "active"}},
			Safeguards: protocol.GatewaySafeguards{LoopbackBound: true, BrowserAuthenticated: true, ControllerIsolated: true, MutationsCoordinated: true},
		},
		panels:  []panel.PanelInfo{{ID: "zashboard", Name: "Zashboard", Active: true, InstalledBuild: "v1", Health: "active"}},
		openURL: "http://127.0.0.1:9191/?token=web-secret-token-value",
	}
	server := New(Options{Token: "token", Store: state.NewStore(fake.snapshot), Runtime: fake})

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodGet, "/v1/web-gui", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	raw := response.Body.String()
	for _, forbidden := range []string{"web-secret", "token=", "open_url", "controller_secret", "credential"} {
		if strings.Contains(strings.ToLower(raw), strings.ToLower(forbidden)) {
			t.Fatalf("web-gui status leaked %q: %s", forbidden, raw)
		}
	}
	var status protocol.WebGUIStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil || status.ActivePanel != "zashboard" {
		t.Fatalf("status=%#v err=%v", status, err)
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodGet, "/v1/panels", nil))
	var list protocol.PanelList
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil || len(list.Panels) != 1 || list.Panels[0].ID != "zashboard" {
		t.Fatalf("list=%#v err=%v", list, err)
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/panels/zashboard/install", bytes.NewBufferString(`{"operation_id":"p1"}`)))
	if response.Code != http.StatusOK || fake.lastID != "zashboard" || fake.lastOp.ID != "p1" {
		t.Fatalf("install status=%d id=%s op=%#v body=%s", response.Code, fake.lastID, fake.lastOp, response.Body.String())
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPut, "/v1/panels/zashboard/active", bytes.NewBufferString(`{"operation_id":"p2"}`)))
	if response.Code != http.StatusOK || fake.lastAct != "zashboard" {
		t.Fatalf("activate status=%d act=%s body=%s", response.Code, fake.lastAct, response.Body.String())
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/panels/zashboard/update", bytes.NewBufferString(`{"operation_id":"p3"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/panels/zashboard/rollback", bytes.NewBufferString(`{"operation_id":"p4"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("rollback status=%d body=%s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/web-gui/open", nil))
	var open protocol.WebGUIOpenResult
	if err := json.Unmarshal(response.Body.Bytes(), &open); err != nil || open.OpenURL != fake.openURL {
		t.Fatalf("open=%#v err=%v body=%s", open, err, response.Body.String())
	}
}

func TestWebGUIRoutesRequireAuth(t *testing.T) {
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: &panelFakeRuntime{fakeRuntime: &fakeRuntime{}}})
	request := httptest.NewRequest(http.MethodGet, "/v1/web-gui", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}
