package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/mihomo"
	"github.com/mihari-proxy/mihari/internal/panel"
	"github.com/mihari-proxy/mihari/internal/platform"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/web"
)

type fixturePanelAdapter struct {
	id    string
	name  string
	build string
	url   string
}

func (a fixturePanelAdapter) ID() string          { return a.id }
func (a fixturePanelAdapter) DisplayName() string { return a.name }
func (a fixturePanelAdapter) SetupPath(host string) string {
	return "/?hostname=" + host + "&disableUpgrade=true"
}
func (a fixturePanelAdapter) ResolveLatest(context.Context) (string, string, error) {
	return a.build, a.url, nil
}

type trackingController struct {
	selected atomic.Value // string
	upgrade  atomic.Int64
	server   *httptest.Server
	secret   string
}

func newTrackingController(t *testing.T, secret string) *trackingController {
	t.Helper()
	tc := &trackingController{secret: secret}
	tc.selected.Store("DIRECT")
	tc.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/version":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": "v1.19.0"})
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/upgrade"):
			tc.upgrade.Add(1)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/proxies/"):
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Name != "" {
				tc.selected.Store(body.Name)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/proxies":
			_ = json.NewEncoder(w).Encode(map[string]any{"proxies": map[string]any{
				"GLOBAL": map[string]any{"name": "GLOBAL", "type": "Selector", "now": tc.selected.Load(), "all": []string{"DIRECT", "REJECT"}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tc.server.Close)
	return tc
}

func TestWebGatewayAuthProxyRejectInstallActivateRollback(t *testing.T) {
	const webToken = "web-token-integration-aaaaaaaaaaaaaaaaaaaaaaaa"
	const controllerSecret = "controller-secret-integration-bbbbbbbbbbbbbbbbbbbb"
	controller := newTrackingController(t, controllerSecret)

	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	zipV1 := mustPanelZip(t, map[string]string{"index.html": "<html>panel-v1</html>"})
	zipV2 := mustPanelZip(t, map[string]string{"index.html": "<html>panel-v2</html>"})
	assetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.zip":
			_, _ = w.Write(zipV1)
		case "/v2.zip":
			_, _ = w.Write(zipV2)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(assetServer.Close)

	adapter := &mutableAdapter{
		fixturePanelAdapter{id: panel.IDZashboard, name: "Zashboard", build: "v1.0.0", url: assetServer.URL + "/v1.zip"},
	}
	panels, err := panel.Open(panel.ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: assetServer.Client(), AllowHTTP: true,
		Adapters: []panel.Adapter{adapter},
	})
	if err != nil {
		t.Fatal(err)
	}

	store := state.NewStore(state.Snapshot{Health: "ok"})
	manager := runtimeapi.New(runtimeapi.Options{
		Store: store, Panels: panels,
		Controller:   mihomo.NewClient(controller.server.URL, controllerSecret, controller.server.Client()),
		WebGateway:   stubWebGateway{}, // capability optional for this test
		WebOpenToken: webToken,
	})
	mutator := integrationWebMutator{manager: manager}

	gateway, err := web.New(web.Options{
		Addr:          "127.0.0.1:0",
		Auth:          web.Authenticator{WebCredential: webToken, ControllerSecret: controllerSecret},
		ControllerURL: controller.server.URL, ControllerSecret: controllerSecret,
		Panel: panels, Mutator: mutator,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- gateway.Serve(ctx) }()
	base := waitGatewayBase(t, gateway)

	// 1) Unauthenticated API → 401
	resp, err := http.Get(base + "/version")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d", resp.StatusCode)
	}

	// 1b) Authenticated GET proxies version
	req, _ := http.NewRequest(http.MethodGet, base+"/version", nil)
	req.Header.Set("Authorization", "Bearer "+webToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "v1.19.0") {
		t.Fatalf("proxy status=%d body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), controllerSecret) || strings.Contains(string(body), webToken) {
		t.Fatal("secrets leaked through version proxy")
	}

	// 2) POST /upgrade never hits fake mihomo
	req, _ = http.NewRequest(http.MethodPost, base+"/upgrade", nil)
	req.Header.Set("Authorization", "Bearer "+webToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "managed_operation") {
		t.Fatalf("upgrade status=%d body=%s", resp.StatusCode, body)
	}
	if controller.upgrade.Load() != 0 {
		t.Fatal("upgrade reached controller")
	}

	// 3) Select proxy via panel path through coordinator mutator
	req, _ = http.NewRequest(http.MethodPut, base+"/proxies/GLOBAL", strings.NewReader(`{"name":"REJECT"}`))
	req.Header.Set("Authorization", "Bearer "+webToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("select status=%d", resp.StatusCode)
	}
	if got := controller.selected.Load().(string); got != "REJECT" {
		t.Fatalf("selected=%q", got)
	}
	// SelectProxy uses the maintenance lock but may not bump the revision
	// (the coordinator path for panels does). What matters here is that the
	// manager accepted the mutation without error above.

	// 4) Install fixture panel → activate → static index.html
	if err := panels.Install(context.Background(), panel.IDZashboard, ""); err != nil {
		t.Fatal(err)
	}
	if err := panels.Activate(context.Background(), panel.IDZashboard); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest(http.MethodGet, base+"/", nil)
	req.AddCookie(&http.Cookie{Name: web.CookieName, Value: webToken})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "panel-v1") {
		t.Fatalf("static status=%d body=%s", resp.StatusCode, body)
	}

	// 5) Install v2, activate, rollback restores previous
	adapter.build = "v2.0.0"
	adapter.url = assetServer.URL + "/v2.zip"
	if err := panels.Install(context.Background(), panel.IDZashboard, ""); err != nil {
		t.Fatal(err)
	}
	if err := panels.Activate(context.Background(), panel.IDZashboard); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest(http.MethodGet, base+"/", nil)
	req.AddCookie(&http.Cookie{Name: web.CookieName, Value: webToken})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "panel-v2") {
		t.Fatalf("expected v2 body=%s", body)
	}
	if err := panels.Rollback(context.Background(), panel.IDZashboard); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest(http.MethodGet, base+"/", nil)
	req.AddCookie(&http.Cookie{Name: web.CookieName, Value: webToken})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "panel-v1") {
		t.Fatalf("rollback body=%s", body)
	}
	active, err := panels.Active()
	if err != nil || active.Build != "v1.0.0" {
		t.Fatalf("active=%#v err=%v", active, err)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gateway shutdown timeout")
	}
}

type mutableAdapter struct {
	fixturePanelAdapter
}

func (a *mutableAdapter) ResolveLatest(context.Context) (string, string, error) {
	return a.build, a.url, nil
}

type integrationWebMutator struct {
	manager *runtimeapi.Manager
}

func (m integrationWebMutator) SelectProxy(ctx context.Context, group, name string) error {
	return m.manager.SelectProxy(ctx, runtimeapi.Operation{ID: "web-select", Source: "web"}, group, name)
}
func (m integrationWebMutator) CloseConnection(ctx context.Context, id string) error {
	return m.manager.CloseConnection(ctx, runtimeapi.Operation{ID: "web-close", Source: "web"}, id)
}
func (m integrationWebMutator) CloseAllConnections(ctx context.Context) error {
	return m.manager.CloseAllConnections(ctx, runtimeapi.Operation{ID: "web-close-all", Source: "web"})
}
func (m integrationWebMutator) ApplyConfigPatch(ctx context.Context, patch map[string]any) error {
	tunRaw, ok := patch["tun"]
	if !ok {
		return nil
	}
	tun, ok := tunRaw.(map[string]any)
	if !ok {
		return nil
	}
	enable, ok := tun["enable"].(bool)
	if !ok {
		return nil
	}
	op := runtimeapi.Operation{ID: "web-tun", Source: "web"}
	if enable {
		_, err := m.manager.EnableTun(ctx, op, false)
		return err
	}
	_, err := m.manager.DisableTun(ctx, op)
	return err
}

type stubWebGateway struct{}

func (stubWebGateway) Serve(context.Context) error { return nil }
func (stubWebGateway) SessionCount() int           { return 0 }
func (stubWebGateway) ListenAddr() string          { return "127.0.0.1:0" }

func waitGatewayBase(t *testing.T, gateway *web.Server) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if addr := gateway.ListenAddr(); addr != "" && !strings.HasSuffix(addr, ":0") {
			return "http://" + addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("gateway did not bind")
	return ""
}

func mustPanelZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPanelServiceFixtureInstallLayout(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	zipBytes := mustPanelZip(t, map[string]string{"index.html": "<html>ok</html>"})
	asset := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipBytes)
	}))
	t.Cleanup(asset.Close)
	service, err := panel.Open(panel.ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		HTTPClient: asset.Client(), AllowHTTP: true,
		Adapters: []panel.Adapter{
			fixturePanelAdapter{id: panel.IDMetaCubeXD, name: "MetaCubeXD", build: "abc123", url: asset.URL},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Install(context.Background(), panel.IDMetaCubeXD, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(paths.WebRoot, panel.IDMetaCubeXD, "abc123", "index.html")); err != nil {
		t.Fatal(err)
	}
}
