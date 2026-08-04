package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestControllerProxyStripsClientAuthAndInjectsSecret(t *testing.T) {
	const secret = "controller-secret-value-zzzzzzzzzzzzzzzzzzzz"
	var sawAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("X-Test", "ok")
		_, _ = w.Write([]byte(`{"version":"1"}`))
	}))
	defer upstream.Close()

	proxy, err := NewControllerProxy(ProxyOptions{ControllerURL: upstream.URL, ControllerSecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(proxy)
	defer gateway.Close()

	req, err := http.NewRequest(http.MethodGet, gateway.URL+"/version", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer web-token-should-be-stripped")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if sawAuth != "Bearer "+secret {
		t.Fatalf("upstream auth=%q", sawAuth)
	}
	if strings.Contains(string(body), secret) {
		t.Fatalf("response body reflected secret: %s", body)
	}
	if strings.Contains(resp.Header.Get("Authorization"), secret) {
		t.Fatal("response authorization header leaked secret")
	}
}

func TestControllerProxyDoesNotReflectSecretInHeaders(t *testing.T) {
	const secret = "controller-secret-value-yyyyyyyyyyyyyyyyyyyy"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Malicious upstream tries to echo secret.
		w.Header().Set("X-Echo", secret)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	proxy, err := NewControllerProxy(ProxyOptions{ControllerURL: upstream.URL, ControllerSecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(proxy)
	defer gateway.Close()
	resp, err := http.Get(gateway.URL + "/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Echo"); strings.Contains(got, secret) {
		t.Fatalf("secret leaked in header: %q", got)
	}
}

func TestWriteRejectUpgradeNeverImpliesSuccess(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteReject(rec, ActionRejectUpgrade)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "managed_operation") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}
