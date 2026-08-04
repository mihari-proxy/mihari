package web

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

// ProxyOptions configures the secret-injecting reverse proxy to the mihomo controller.
type ProxyOptions struct {
	// ControllerURL is the loopback controller base, e.g. http://127.0.0.1:9090.
	ControllerURL string
	// ControllerSecret is injected as Bearer; never returned to the browser.
	ControllerSecret string
	// Transport is optional for tests.
	Transport http.RoundTripper
}

// NewControllerProxy builds a reverse proxy that strips client Authorization and injects the controller secret.
func NewControllerProxy(options ProxyOptions) (*httputil.ReverseProxy, error) {
	target, err := url.Parse(options.ControllerURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid controller url"}
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	if options.Transport != nil {
		proxy.Transport = options.Transport
	}
	originalDirector := proxy.Director
	secret := options.ControllerSecret
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Strip any browser-supplied auth so the web credential never reaches the controller.
		req.Header.Del("Authorization")
		if secret != "" {
			req.Header.Set("Authorization", "Bearer "+secret)
		}
		// Mihomo often expects Host of the controller.
		req.Host = target.Host
	}
	// Do not rewrite Location in a way that exposes controller host if avoidable; default is fine for loopback.
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Never forward controller secret in response headers.
		for name, values := range resp.Header {
			lower := strings.ToLower(name)
			if lower == "authorization" {
				resp.Header.Del(name)
				continue
			}
			for _, value := range values {
				if secret != "" && strings.Contains(value, secret) {
					resp.Header.Del(name)
					break
				}
			}
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "upstream controller unavailable", http.StatusBadGateway)
	}
	return proxy, nil
}

// WriteReject writes a JSON error for a classified reject action without contacting mihomo.
func WriteReject(w http.ResponseWriter, action Action) {
	code := protocol.CodeUnsupportedMutation
	message := "unsupported web mutation"
	status := http.StatusForbidden
	switch action {
	case ActionRejectUpgrade:
		code = protocol.CodeManagedOperation
		message = "core upgrade is managed by mihari"
	case ActionRejectManaged:
		code = protocol.CodeManagedField
		message = "field is managed by mihari"
	case ActionRejectUnknown:
		code = protocol.CodeUnsupportedMutation
		message = "unsupported web mutation"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	envelope := protocol.NewError(code, message, nil)
	// Best-effort encode without importing encoding/json cycles; small fixed body is fine.
	_, _ = w.Write([]byte(`{"schema":"` + envelope.Schema + `","error":{"code":"` + string(code) + `","message":"` + message + `"}}`))
}
