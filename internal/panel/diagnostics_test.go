package panel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type diagnosticTransport func(*http.Request) (*http.Response, error)

func (f diagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type diagnosticReadBody struct {
	cause  error
	closed bool
}

func (b *diagnosticReadBody) Read([]byte) (int, error) { return 0, b.cause }
func (b *diagnosticReadBody) Close() error             { b.closed = true; return nil }
func TestPanelDiagnostic_DownloadPreservesSafeCause(t *testing.T) {
	for _, stage := range []string{"request", "transport", "read"} {
		t.Run(stage, func(t *testing.T) {
			cause := &os.PathError{Op: "open", Path: "/private/business-secret", Err: os.ErrPermission}
			body := &diagnosticReadBody{cause: cause}
			service := &Service{stagingDir: t.TempDir(), maxBytes: 1024, httpClient: &http.Client{Transport: diagnosticTransport(func(*http.Request) (*http.Response, error) {
				if stage == "transport" {
					return nil, cause
				}
				return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header)}, nil
			})}}
			asset := "https://example.test/panel?token=business-secret"
			if stage == "request" {
				asset = ":invalid-business-secret"
			}
			_, err := service.download(context.Background(), "fixture", "v1", asset)
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
				t.Fatalf("download cause lost: %v", err)
			}
			if stage == "read" && !body.closed {
				t.Fatal("response body not closed")
			}
			entries, readErr := os.ReadDir(service.stagingDir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Fatal("failed download left candidate")
			}
		})
	}
}

var _ io.ReadCloser = (*diagnosticReadBody)(nil)
