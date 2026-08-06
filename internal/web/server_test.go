package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingMutator captures ApplyConfigPatch calls for allowlist tests.
type recordingMutator struct {
	mu      sync.Mutex
	patches []map[string]any
	err     error
}

func (m *recordingMutator) SelectProxy(context.Context, string, string) error { return nil }
func (m *recordingMutator) CloseConnection(context.Context, string) error     { return nil }
func (m *recordingMutator) CloseAllConnections(context.Context) error         { return nil }

func (m *recordingMutator) ApplyConfigPatch(_ context.Context, patch map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make(map[string]any, len(patch))
	for k, v := range patch {
		copied[k] = v
	}
	m.patches = append(m.patches, copied)
	return m.err
}

func (m *recordingMutator) lastPatch() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.patches) == 0 {
		return nil
	}
	return m.patches[len(m.patches)-1]
}

type memoryPanel struct {
	dir    string
	path   string
	panels map[string]string // panelID → static root
	setups map[string]string // panelID → setup path
}

func (m memoryPanel) ActiveDir() (string, error) { return m.dir, nil }
func (m memoryPanel) SetupPath(host string) string {
	if m.path != "" {
		return m.path
	}
	// Mirror Zashboard-style same-origin setup deep-link (host may be host:port).
	hostname, port := host, ""
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host[:i], ":") {
		hostname, port = host[:i], host[i+1:]
	}
	path := "/__mihari/panels/zashboard/#/setup?hostname=" + hostname + "&disableUpgrade=true"
	if port != "" {
		path = "/__mihari/panels/zashboard/#/setup?hostname=" + hostname + "&port=" + port + "&disableUpgrade=true"
	}
	return path
}
func (m memoryPanel) PanelDir(panelID string) (string, error) {
	if m.panels != nil {
		return m.panels[panelID], nil
	}
	return "", nil
}
func (m memoryPanel) SetupPathFor(panelID, host string) string {
	if m.setups != nil {
		if setup := m.setups[panelID]; setup != "" {
			return setup
		}
	}
	return "/__mihari/panels/" + panelID + "/#/setup?hostname=" + host
}

func TestGatewayProxiesVersionWithAuthAndRejectsUpgrade(t *testing.T) {
	const webToken = "web-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const controllerSecret = "controller-secret-bbbbbbbbbbbbbbbbbbbbbbbb"
	var sawUpgrade bool
	var sawAuth string
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upgrade" {
			sawUpgrade = true
			w.WriteHeader(http.StatusOK)
			return
		}
		sawAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"version":"v1"}`))
	}))
	defer controller.Close()

	uiDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(uiDir, "index.html"), []byte("<html>panel</html>"), 0o600); err != nil {
		t.Fatal(err)
	}

	gateway, err := New(Options{
		Addr:          "127.0.0.1:0",
		Auth:          Authenticator{WebCredential: webToken, ControllerSecret: controllerSecret},
		ControllerURL: controller.URL, ControllerSecret: controllerSecret,
		Panel: memoryPanel{dir: uiDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- gateway.Serve(ctx) }()
	// Wait until bound.
	deadline := time.Now().Add(2 * time.Second)
	var base string
	for time.Now().Before(deadline) {
		if addr := gateway.ListenAddr(); addr != "" && addr != "127.0.0.1:0" {
			base = "http://" + addr
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if base == "" {
		t.Fatal("gateway did not bind")
	}

	// Unauthenticated API → 401.
	resp, err := http.Get(base + "/version")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d", resp.StatusCode)
	}

	// Authenticated GET proxies with injected controller secret.
	req, _ := http.NewRequest(http.MethodGet, base+"/version", nil)
	req.Header.Set("Authorization", "Bearer "+webToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "v1") {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if sawAuth != "Bearer "+controllerSecret {
		t.Fatalf("controller auth=%q", sawAuth)
	}
	if strings.Contains(string(body), controllerSecret) {
		t.Fatal("controller secret leaked to browser")
	}

	// POST /upgrade rejected without hitting controller.
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
	if sawUpgrade {
		t.Fatal("upgrade reached controller")
	}

	// Static panel with cookie session.
	req, _ = http.NewRequest(http.MethodGet, base+"/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: webToken})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "panel") {
		t.Fatalf("static status=%d body=%s", resp.StatusCode, body)
	}

	// Credential-less module/script fetch (Vite crossorigin) must still get static bytes.
	if err := os.WriteFile(filepath.Join(uiDir, "index-BcsehOF5.js"), []byte("export default 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest(http.MethodGet, base+"/index-BcsehOF5.js", nil)
	req.Header.Set("Sec-Fetch-Dest", "script")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "export default") {
		t.Fatalf("credential-less script status=%d body=%s", resp.StatusCode, body)
	}
	// Unauthenticated document GET serves panel HTML (API remains gated); no login wall for SPA shells.
	req, _ = http.NewRequest(http.MethodGet, base+"/", nil)
	req.Header.Set("Sec-Fetch-Dest", "document")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "panel") {
		t.Fatalf("unauth document status=%d body=%s", resp.StatusCode, body)
	}
	// Missing .js must 404 as application error, not SPA index.html (wrong MIME for modules).
	resp, err = http.Get(base + "/missing-module.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing module status=%d", resp.StatusCode)
	}

	// One-shot token sets cookie and redirects to panel setup (no token, no :9090).
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err = client.Get(base + "/?token=" + webToken)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("token redirect status=%d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if strings.Contains(loc, "token=") {
		t.Fatalf("redirect kept token: %s", loc)
	}
	if !strings.Contains(loc, "/#/setup?") {
		t.Fatalf("token open should land on setup deep-link, got %q", loc)
	}
	if strings.Contains(loc, "9090") {
		t.Fatalf("setup must not target controller port: %s", loc)
	}
	if len(resp.Cookies()) == 0 || resp.Cookies()[0].Name != CookieName {
		t.Fatalf("cookies=%v", resp.Cookies())
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gateway did not shut down")
	}
}

func TestGatewayServesInstalledPanelsConcurrentlyAtUIPaths(t *testing.T) {
	const webToken = "web-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const controllerSecret = "controller-secret-bbbbbbbbbbbbbbbbbbbbbbbb"
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"v1"}`))
	}))
	defer controller.Close()

	root := t.TempDir()
	zashDir := filepath.Join(root, "zashboard")
	metaDir := filepath.Join(root, "metacubexd")
	if err := os.MkdirAll(zashDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zashDir, "index.html"), []byte("<html>zashboard</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "index.html"), []byte("<html>metacubexd</html>"), 0o600); err != nil {
		t.Fatal(err)
	}

	gateway, err := New(Options{
		Addr:          "127.0.0.1:0",
		Auth:          Authenticator{WebCredential: webToken, ControllerSecret: controllerSecret},
		ControllerURL: controller.URL, ControllerSecret: controllerSecret,
		Panel: memoryPanel{
			dir: zashDir,
			panels: map[string]string{
				"zashboard":  zashDir,
				"metacubexd": metaDir,
			},
			setups: map[string]string{
				"zashboard":  "/__mihari/panels/zashboard/#/setup?hostname=127.0.0.1:9191",
				"metacubexd": "/__mihari/panels/metacubexd/#/setup?hostname=127.0.0.1:9191&http=true",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = gateway.Serve(ctx) }()
	base := waitGatewayBase(t, gateway)

	for _, tc := range []struct {
		path string
		want string
	}{
		{"/__mihari/panels/zashboard/", "zashboard"},
		{"/__mihari/panels/metacubexd/", "metacubexd"},
		{"/", "zashboard"}, // default active remains available at root
	} {
		req, _ := http.NewRequest(http.MethodGet, base+tc.path, nil)
		req.AddCookie(&http.Cookie{Name: CookieName, Value: webToken})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), tc.want) {
			t.Fatalf("path=%s status=%d body=%s", tc.path, resp.StatusCode, body)
		}
	}

	// Opening metacubexd lands on metacubexd setup, not the default zashboard.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(base + "/__mihari/panels/metacubexd/?token=" + webToken)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/__mihari/panels/metacubexd/") || strings.Contains(loc, "zashboard") || strings.Contains(loc, "token=") {
		t.Fatalf("open metacubexd location=%q", loc)
	}

	// Panel assets under /__mihari/panels/{id}/ load without a cookie (crossorigin omit).
	if err := os.WriteFile(filepath.Join(zashDir, "app.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zashDir, "manifest.webmanifest"), []byte(`{"name":"z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Vite base "/" index: rewrite root-absolute assets onto the panel mount.
	if err := os.WriteFile(filepath.Join(zashDir, "index.html"), []byte(
		`<html><script type="module" crossorigin src="/assets/index-BcsehOF5.js"></script>`+
			`<link rel="manifest" href="/manifest.webmanifest"><body>zashboard</body></html>`,
	), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/__mihari/panels/zashboard/app.js",
		"/__mihari/panels/zashboard/manifest.webmanifest",
		"/__mihari/panels/zashboard/index.html",
	} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("path=%s status=%d body=%s", path, resp.StatusCode, body)
		}
		if strings.HasSuffix(path, "index.html") {
			if !strings.Contains(string(body), `/__mihari/panels/zashboard/assets/index-BcsehOF5.js`) {
				t.Fatalf("index missing rewritten asset path: %s", body)
			}
			if strings.Contains(string(body), `src="/assets/`) {
				t.Fatalf("index still has root-absolute asset: %s", body)
			}
		}
	}
	// Root-absolute asset with panel Referer resolves from that panel tree (not only active).
	if err := os.WriteFile(filepath.Join(metaDir, "only-meta.js"), []byte("meta"), 0o600); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/only-meta.js", nil)
	req.Header.Set("Referer", base+"/__mihari/panels/metacubexd/")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "meta" {
		t.Fatalf("referer panel asset status=%d body=%s", resp.StatusCode, body)
	}
	// API under the same origin still requires auth.
	resp, err = http.Get(base + "/version")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth api status=%d", resp.StatusCode)
	}
}

func TestRewriteRootAbsoluteURLs(t *testing.T) {
	in := []byte(`<script src="/assets/a.js"></script><link href="//cdn.example/x.css"><a href="/__mihari/panels/zashboard/ok">`)
	out := string(rewriteRootAbsoluteURLs(in, "/__mihari/panels/zashboard"))
	if !strings.Contains(out, `src="/__mihari/panels/zashboard/assets/a.js"`) {
		t.Fatalf("out=%s", out)
	}
	if !strings.Contains(out, `href="//cdn.example/x.css"`) {
		t.Fatalf("protocol-relative rewritten: %s", out)
	}
	if !strings.Contains(out, `href="/__mihari/panels/zashboard/ok"`) {
		t.Fatalf("existing mount broken: %s", out)
	}
}

func TestGatewayWrongControllerSecretDoesNotAuthorize(t *testing.T) {
	auth := Authenticator{
		WebCredential:    "web-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ControllerSecret: "controller-secret-bbbbbbbbbbbbbbbbbbbbbbbb",
	}
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	req.Header.Set("Authorization", "Bearer "+auth.ControllerSecret)
	if auth.Authorized(req) {
		t.Fatal("controller secret must not authorize")
	}
}

func TestGatewayConfigPatchAllowlistTUN(t *testing.T) {
	const webToken = "web-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const controllerSecret = "controller-secret-bbbbbbbbbbbbbbbbbbbbbbbb"

	var gotConfigsPath bool
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/configs" {
			gotConfigsPath = true
			_, _ = w.Write([]byte(`{"mode":"rule","tun":{"enable":false}}`))
			return
		}
		// Mutations must not reach the controller.
		t.Errorf("unexpected controller hit: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer controller.Close()

	mutator := &recordingMutator{}
	gateway, err := New(Options{
		Addr:             "127.0.0.1:0",
		Auth:             Authenticator{WebCredential: webToken, ControllerSecret: controllerSecret},
		ControllerURL:    controller.URL,
		ControllerSecret: controllerSecret,
		Mutator:          mutator,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- gateway.Serve(ctx) }()
	base := waitGatewayBase(t, gateway)

	authHeader := func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+webToken)
		req.Header.Set("Content-Type", "application/json")
	}

	// PATCH {"tun":{"enable":true}} → mutator called, 204.
	req, _ := http.NewRequest(http.MethodPatch, base+"/configs", strings.NewReader(`{"tun":{"enable":true}}`))
	authHeader(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("tun enable status=%d body=%s", resp.StatusCode, body)
	}
	patch := mutator.lastPatch()
	if patch == nil {
		t.Fatal("ApplyConfigPatch not called")
	}
	tun, ok := patch["tun"].(map[string]any)
	if !ok {
		t.Fatalf("patch=%#v", patch)
	}
	if enable, _ := tun["enable"].(bool); !enable {
		t.Fatalf("tun=%#v", tun)
	}

	// PATCH with secret/controller → reject managed.
	for _, payload := range []string{
		`{"secret":"x"}`,
		`{"external-controller":"127.0.0.1:1"}`,
		`{"tun":{"enable":true},"secret":"x"}`,
		`{"mixed-port":7890}`,
		`{"bind-address":"*"}`,
		`{"external-ui":"/tmp"}`,
		`{"external-ui-name":"z"}`,
	} {
		req, _ = http.NewRequest(http.MethodPatch, base+"/configs", strings.NewReader(payload))
		authHeader(req)
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "managed_field") {
			t.Fatalf("managed payload %s status=%d body=%s", payload, resp.StatusCode, body)
		}
	}

	// PATCH with unknown key → reject unsupported.
	req, _ = http.NewRequest(http.MethodPatch, base+"/configs", strings.NewReader(`{"mode":"global"}`))
	authHeader(req)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "unsupported_mutation") {
		t.Fatalf("unknown key status=%d body=%s", resp.StatusCode, body)
	}

	// Empty body / empty object rejected.
	req, _ = http.NewRequest(http.MethodPatch, base+"/configs", strings.NewReader(`{}`))
	authHeader(req)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		t.Fatal("empty patch must not succeed")
	}

	// GET /configs still proxies read.
	req, _ = http.NewRequest(http.MethodGet, base+"/configs", nil)
	req.Header.Set("Authorization", "Bearer "+webToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"mode"`) {
		t.Fatalf("GET configs status=%d body=%s", resp.StatusCode, body)
	}
	if !gotConfigsPath {
		t.Fatal("GET /configs did not reach controller")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gateway did not shut down")
	}
}

func waitGatewayBase(t *testing.T, gateway *Server) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if addr := gateway.ListenAddr(); addr != "" && addr != "127.0.0.1:0" {
			return "http://" + addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("gateway did not bind")
	return ""
}
