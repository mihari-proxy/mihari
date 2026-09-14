package web

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

// ProxyOptions configures the secret-injecting reverse proxy to the mihomo controller.
type ProxyOptions struct {
	Reporter diagnostics.Reporter
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
	if err != nil {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid controller url"}, err)
	}
	if target.Scheme == "" || target.Host == "" {
		return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid controller url"}
	}
	secret := options.ControllerSecret
	// Rewrite replaces Director (deprecated since Go 1.26); a nil Transport
	// falls back to http.DefaultTransport. Rewrite fully overrides the
	// single-host rewrite, so SetURL must be called explicitly.
	proxy := &httputil.ReverseProxy{
		// The response observer owns upstream read diagnostics. The default
		// ReverseProxy logger would duplicate them outside the file reporter.
		ErrorLog:  log.New(io.Discard, "", 0),
		Transport: controllerTransport{options.Transport},
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// Strip any browser-supplied auth so the web credential never reaches the controller.
			pr.Out.Header.Del("Authorization")
			if secret != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+secret)
			}
			// Mihomo often expects Host of the controller.
			pr.Out.Host = target.Host
		},
	}
	// Do not rewrite Location in a way that exposes controller host if avoidable; default is fine for loopback.
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.Body != nil {
			resp.Body = &observedHTTPBody{ReadCloser: resp.Body, ctx: resp.Request.Context(), reporter: options.Reporter,
				detail: diagnostics.HTTPError{Operation: diagnostics.HTTPOperation(resp.Request.Method, resp.Request.URL.Path), URL: resp.Request.URL.String(), Phase: "response", Status: resp.StatusCode}}
		}
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
		reportFailure(r.Context(), options.Reporter, "proxy.failed", (&diagnostics.HTTPError{Operation: diagnostics.HTTPOperation(r.Method, r.URL.Path), URL: r.URL.String(), Phase: "transport", Cause: err}))
		http.Error(w, "upstream controller unavailable", http.StatusBadGateway)
	}
	return proxy, nil
}

type controllerTransport struct{ transport http.RoundTripper }

func (t controllerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport := t.transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	response, err := transport.RoundTrip(request)
	if response != nil && response.Request == nil {
		response.Request = request
	}
	return response, err
}

// reportFailure is used only by the owner returning a final gateway outcome.
func reportFailure(ctx context.Context, reporter diagnostics.Reporter, event string, err error) {
	if reporter == nil || diagnostics.AlreadyReported(err) {
		return
	}
	if level, emit := diagnostics.FailureLevel(ctx, err); emit {
		reporter(ctx, diagnostics.Record{Component: "web", Event: event, Level: level, Err: err})
	}
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
