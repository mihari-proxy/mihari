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
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type diagnosticTransport func(*http.Request) (*http.Response, error)

func (f diagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type diagnosticReadBody struct {
	cause     error
	closed    bool
	content   io.Reader
	closeErr  error
	readBytes int
}

func (b *diagnosticReadBody) Read(p []byte) (int, error) {
	if b.content != nil {
		n, err := b.content.Read(p)
		b.readBytes += n
		return n, err
	}
	return 0, b.cause
}
func (b *diagnosticReadBody) Close() error { b.closed = true; return b.closeErr }
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

func TestPanelDiagnostic_StatusBodyAndCloseCausePreserved(t *testing.T) {
	closeCause := errors.New("close panel response")
	body := &diagnosticReadBody{content: strings.NewReader("token=fixture\n" + strings.Repeat("x", diagnostics.MaxHTTPBodyBytes)), closeErr: closeCause}
	service := &Service{stagingDir: t.TempDir(), maxBytes: 1024, httpClient: &http.Client{Transport: diagnosticTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Body: body, Header: make(http.Header), Request: r}, nil
	})}}
	_, err := service.download(context.Background(), "fixture", "v1", "https://fixture.invalid/panel?token=fixture")
	var detail *diagnostics.HTTPError
	if !errors.As(err, &detail) || !strings.HasPrefix(detail.Body, "token=fixture\n") || !strings.Contains(detail.Body, "[truncated]") || !detail.BodyTruncated || !errors.Is(err, closeCause) {
		t.Fatalf("status body or close cause lost: %v", err)
	}
	if body.readBytes != diagnostics.MaxHTTPBodyBytes+1 || !body.closed {
		t.Fatalf("body accounting changed: bytes=%d closed=%t", body.readBytes, body.closed)
	}
}
