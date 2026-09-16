package mihomo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type countedFailureBody struct {
	io.Reader
	read, closed int
}

func (b *countedFailureBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}

func (b *countedFailureBody) Close() error { b.closed++; return nil }

func TestHTTPDiagnostics_FailureBodyReadIsBoundedAtCollection(t *testing.T) {
	body := &countedFailureBody{Reader: strings.NewReader(strings.Repeat("x", 1<<20))}
	c := NewClient("http://127.0.0.1", "", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: make(http.Header), Body: body}, nil
	})})
	_, err := c.Version(context.Background())
	var detail *diagnostics.HTTPError
	if !errors.As(err, &detail) || len(detail.Body) != diagnostics.MaxHTTPBodyBytes || !strings.HasSuffix(detail.Body, " [truncated]") || !detail.BodyTruncated {
		t.Fatal("HTTP failure did not retain the bounded diagnostic body")
	}
	if body.read != diagnostics.MaxHTTPBodyBytes+1 || body.closed != 1 {
		t.Fatalf("failure body read=%d closed=%d", body.read, body.closed)
	}
}
