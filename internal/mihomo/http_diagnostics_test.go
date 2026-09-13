package mihomo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

type closeDiagnosticBody struct {
	io.Reader
	err error
}

func (b closeDiagnosticBody) Close() error { return b.err }

func TestHTTPDiagnostics_SuccessfulCloseWarningRedactsShortCredential(t *testing.T) {
	var out bytes.Buffer
	report := logging.NewDiagnosticReporter(slog.New(slog.NewTextHandler(&out, nil)), logging.NewRedactor())
	c := NewClient("http://127.0.0.1", "tiny", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: closeDiagnosticBody{strings.NewReader(`{"version":"fixture"}`), errors.New("close failed tiny")}}, nil
	})})
	c.SetDiagnosticReporter(report)
	result, err := c.Version(context.Background())
	if err != nil || result.Version != "fixture" {
		t.Fatal("close failure changed successful operation")
	}
	if !strings.Contains(out.String(), "WARN") || !strings.Contains(out.String(), "close failed") || strings.Contains(out.String(), "tiny") {
		t.Fatal("close diagnostic missing or unsafe")
	}
}

func TestHTTPDiagnostics_DecodeCauseWithoutSuccessfulBodyDump(t *testing.T) {
	c := NewClient("http://127.0.0.1", "", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"version":1,"unused":"do-not-dump"}`))}, nil
	})})
	_, err := c.Version(context.Background())
	var cause *json.UnmarshalTypeError
	var detail *diagnostics.HTTPError
	if !errors.As(err, &cause) || !errors.As(err, &detail) || !strings.Contains(detail.DiagnosticText(), "cannot unmarshal") || strings.Contains(detail.DiagnosticText(), "do-not-dump") {
		t.Fatal("decode diagnostic lost or successful payload dumped")
	}
}

func TestHTTPDiagnostics_HandshakeHasStatusAndOriginalBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "handshake rejected", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	err := NewClient(upstream.URL, "", upstream.Client()).Stream(context.Background(), StreamTraffic, func(json.RawMessage) error { return nil })
	var detail *diagnostics.HTTPError
	if !errors.As(err, &detail) || detail.Status != 503 || !strings.Contains(detail.DiagnosticText(), "handshake rejected") {
		t.Fatal("handshake lost HTTP details")
	}
	if strings.Contains(err.Error(), "handshake rejected") {
		t.Fatal("handshake leaked raw body to public message")
	}
}

func TestProxyProvider_HealthcheckEscapesBothIdentities(t *testing.T) {
	c := NewClient("http://127.0.0.1", "", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.EscapedPath() != "/providers/proxies/a%2Fb/node%3F%23%2Fx/healthcheck" {
			t.Fatalf("path=%s", r.URL.EscapedPath())
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"delay":12}`))}, nil
	})})
	delay, err := c.DelayProviderProxy(context.Background(), "a/b", "node?#/x", "https://test.invalid/204", 5000)
	if err != nil || delay != 12 {
		t.Fatal("escaped provider delay failed")
	}
}

func TestProxyProvider_MissingCatalogIsNotEmptySuccess(t *testing.T) {
	c := NewClient("http://127.0.0.1", "", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})})
	_, err := c.ProxyProviders(context.Background())
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure {
		t.Fatal("missing provider catalog accepted as successful empty discovery")
	}
}

func TestHTTPDiagnostics_StatusBodyAndPublicBoundary(t *testing.T) {
	c := NewClient("http://127.0.0.1", "tiny", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"message":"proxy not found","token":"abc","secret":"tiny"}`))}, nil
	})})
	_, err := c.Version(context.Background())
	var api protocol.APIError
	if !errors.As(err, &api) || api.Details["status"] != 404 {
		t.Fatalf("lost status: %v", err)
	}
	if strings.Contains(err.Error(), "proxy not found") {
		t.Fatal("raw body leaked to public error")
	}
	var out bytes.Buffer
	redactor := logging.NewRedactor()
	report := logging.NewDiagnosticReporter(slog.New(slog.NewTextHandler(&out, nil)), redactor)
	report(context.Background(), diagnostics.Record{Err: err, Level: slog.LevelError, Event: "test"})
	for _, want := range []string{"404", "proxy not found", "version"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in diagnostic", want)
		}
	}
	for _, secret := range []string{"tiny", "abc"} {
		if strings.Contains(out.String(), secret) {
			t.Error("diagnostic leaked credential")
		}
	}
}
