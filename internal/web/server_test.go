package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type memoryPanel struct {
	dir  string
	path string
}

func (m memoryPanel) ActiveDir() (string, error) { return m.dir, nil }
func (m memoryPanel) SetupPath(host string) string {
	if m.path != "" {
		return m.path
	}
	return "/?hostname=" + host + "&disableUpgrade=true"
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

	// One-shot token sets cookie and redirects without token.
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
	if loc := resp.Header.Get("Location"); strings.Contains(loc, "token=") {
		t.Fatalf("redirect kept token: %s", loc)
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
