package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthAcceptsWebCredentialBearerAndCookie(t *testing.T) {
	auth := Authenticator{WebCredential: "web-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ControllerSecret: "controller-secret-bbbbbbbbbbbbbbbbbbbbbbbb"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+auth.WebCredential)
	if !auth.Authorized(req) {
		t.Fatal("expected bearer web credential to authorize")
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: auth.WebCredential})
	if !auth.Authorized(req) {
		t.Fatal("expected cookie web credential to authorize")
	}
}

func TestAuthRejectsControllerSecretAsWebPassword(t *testing.T) {
	auth := Authenticator{
		WebCredential:    "web-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ControllerSecret: "controller-secret-bbbbbbbbbbbbbbbbbbbbbbbb",
	}
	if !auth.RejectsControllerSecretAsPassword() {
		t.Fatal("expected controller secret to differ from web credential")
	}
	if auth.AuthenticateForm(auth.ControllerSecret) {
		t.Fatal("controller secret must not authenticate as web password")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+auth.ControllerSecret)
	if auth.Authorized(req) {
		t.Fatal("controller secret bearer must not authorize web gateway")
	}
}

func TestAuthRejectsMissingAndWrongToken(t *testing.T) {
	auth := Authenticator{WebCredential: "web-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if auth.Authorized(req) {
		t.Fatal("expected unauthenticated request")
	}
	req.Header.Set("Authorization", "Bearer wrong")
	if auth.Authorized(req) {
		t.Fatal("expected wrong token rejection")
	}
}

func TestSetSessionCookieIsHttpOnly(t *testing.T) {
	auth := Authenticator{WebCredential: "web-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	rec := httptest.NewRecorder()
	auth.SetSessionCookie(rec)
	cookie := rec.Result().Cookies()
	if len(cookie) != 1 || cookie[0].Name != CookieName || !cookie[0].HttpOnly {
		t.Fatalf("cookies=%v", cookie)
	}
	if strings.Contains(strings.ToLower(rec.Header().Get("Set-Cookie")), "secret") {
		t.Fatal("cookie header should not mention controller secret")
	}
}
