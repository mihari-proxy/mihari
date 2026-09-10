package mihomo

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type providerDiagnosticBody struct {
	err    error
	closed bool
}

func (b *providerDiagnosticBody) Read([]byte) (int, error) { return 0, b.err }
func (b *providerDiagnosticBody) Close() error             { b.closed = true; return nil }
func TestProviderDiagnostic_AdapterKeepsCause(t *testing.T) {
	for _, stage := range []string{"request", "transport", "read"} {
		t.Run(stage, func(t *testing.T) {
			cause := &os.PathError{Op: "open", Path: "/private/business-secret", Err: os.ErrPermission}
			body := &providerDiagnosticBody{err: cause}
			base := "http://127.0.0.1"
			if stage == "request" {
				base = ":invalid-business-secret"
			}
			c := NewClient(base, "controller-secret", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPut || r.URL.EscapedPath() != "/providers/rules/AI%2FSearch" {
					t.Errorf("wrong native provider request: %s %s", r.Method, r.URL.EscapedPath())
				}
				if stage == "transport" {
					return nil, cause
				}
				return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header)}, nil
			})})
			err := c.UpdateRuleProvider(context.Background(), "AI/Search")
			var api protocol.APIError
			if !errors.As(err, &api) || strings.Contains(err.Error(), "business-secret") {
				t.Fatalf("public error changed: %v", err)
			}
			if stage == "request" {
				var urlErr *url.Error
				if !errors.As(err, &urlErr) {
					t.Fatalf("request cause lost: %v", err)
				}
			} else if !errors.Is(err, cause) {
				t.Fatalf("upstream cause lost: %v", err)
			}
			if stage == "read" && !body.closed {
				t.Fatal("body not closed")
			}
		})
	}
}
