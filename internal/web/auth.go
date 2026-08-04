package web

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"
)

const (
	// CookieName is the HttpOnly session cookie for the Web gateway.
	CookieName = "mihari_web_auth"
	// AuthPath is the login endpoint reserved under a mihari prefix to avoid panel SPA clashes.
	AuthPath = "/__mihari/auth"
)

// Authenticator validates browser access using the dedicated Web credential.
// The controller secret is never accepted as a Web password.
type Authenticator struct {
	// WebCredential is the daemon-owned browser access token (not the controller secret).
	WebCredential string
	// ControllerSecret is retained only to reject mistaken use as a Web password in tests/docs;
	// authentication never succeeds by matching the controller secret alone when it differs.
	ControllerSecret string
	// CookieSecure sets the Secure flag (false for local http://127.0.0.1).
	CookieSecure bool
	// SessionTTL bounds cookie Max-Age.
	SessionTTL time.Duration
}

// Authorized reports whether the request carries a valid Web session cookie or Bearer token.
func (a Authenticator) Authorized(r *http.Request) bool {
	if a.WebCredential == "" {
		return false
	}
	if token := bearerToken(r.Header.Get("Authorization")); token != "" {
		return a.matchesWeb(token)
	}
	if cookie, err := r.Cookie(CookieName); err == nil {
		return a.matchesWeb(cookie.Value)
	}
	// One-shot open-browser token on any path.
	if token := r.URL.Query().Get("token"); token != "" {
		return a.matchesWeb(token)
	}
	return false
}

// AuthenticateForm validates a posted password field against the Web credential only.
func (a Authenticator) AuthenticateForm(password string) bool {
	return a.matchesWeb(password)
}

// SetSessionCookie writes the Web session cookie.
func (a Authenticator) SetSessionCookie(w http.ResponseWriter) {
	ttl := a.SessionTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    a.WebCredential,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.CookieSecure,
		MaxAge:   int(ttl.Seconds()),
	})
}

// ClearSessionCookie expires the Web session cookie.
func (a Authenticator) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.CookieSecure,
		MaxAge:   -1,
	})
}

func (a Authenticator) matchesWeb(candidate string) bool {
	if a.WebCredential == "" || candidate == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(candidate), []byte(a.WebCredential)) != 1 {
		return false
	}
	// If someone configured the same random value for both (misconfiguration), still accept:
	// authentication is "matches web credential", not "differs from controller".
	return true
}

// RejectsControllerSecretAsPassword documents that using only the controller secret fails
// when it is not equal to the Web credential.
func (a Authenticator) RejectsControllerSecretAsPassword() bool {
	if a.ControllerSecret == "" || a.WebCredential == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(a.ControllerSecret), []byte(a.WebCredential)) != 1
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}
