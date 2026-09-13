package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type partialDiagnosticBody struct {
	data              []byte
	readErr, closeErr error
	reads, closes     int
}

func (b *partialDiagnosticBody) Read(p []byte) (int, error) {
	b.reads++
	n := copy(p, b.data)
	b.data = b.data[n:]
	if len(b.data) == 0 {
		return n, b.readErr
	}
	return n, nil
}
func (b *partialDiagnosticBody) Close() error { b.closes++; return b.closeErr }

func TestHTTPBody_ObserveOnlyConsumedBytesAndCloseOnce(t *testing.T) {
	original := bytes.Repeat([]byte{0x1f, 0x8b, 'a'}, 30000)
	source := &partialDiagnosticBody{data: append([]byte(nil), original...), readErr: io.ErrUnexpectedEOF, closeErr: errors.New("close failed")}
	var records []diagnostics.Record
	body := &observedHTTPBody{ReadCloser: source, ctx: context.Background(), reporter: func(_ context.Context, r diagnostics.Record) { records = append(records, r) }, detail: diagnostics.HTTPError{Status: 503, Phase: "response"}}
	if source.reads != 0 {
		t.Fatal("observer eagerly read body")
	}
	got, err := io.ReadAll(body)
	if !errors.Is(err, io.ErrUnexpectedEOF) || !bytes.Equal(got, original) {
		t.Fatal("changed compressed/partial byte stream")
	}
	_ = body.Close()
	_ = body.Close()
	if source.closes != 1 || len(records) != 1 || len(body.raw) > 64<<10 {
		t.Fatal("unbounded capture or duplicate close/report")
	}
	detail := records[0].Err.(*diagnostics.HTTPError)
	if !strings.Contains(detail.DiagnosticText(), "truncated") || !errors.Is(detail, io.ErrUnexpectedEOF) || !strings.Contains(detail.DiagnosticText(), "close failed") {
		t.Fatal("partial body lost diagnostic causes")
	}
}

func TestHTTPBody_SuccessNoDumpAndCanceledReadQuiet(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		source := &partialDiagnosticBody{data: []byte("successful private config"), readErr: io.EOF}
		if cancelled {
			cancel()
			source.readErr = context.Canceled
		}
		var records []diagnostics.Record
		body := &observedHTTPBody{ReadCloser: source, ctx: ctx, reporter: func(_ context.Context, r diagnostics.Record) { records = append(records, r) }, detail: diagnostics.HTTPError{Status: 200}}
		_, _ = io.ReadAll(body)
		_ = body.Close()
		cancel()
		if len(records) != 0 || len(body.raw) != 0 {
			t.Fatal("successful/canceled read logged or dumped body")
		}
	}
	source := &partialDiagnosticBody{readErr: io.EOF, closeErr: errors.New("close failure")}
	var level slog.Level
	body := &observedHTTPBody{ReadCloser: source, ctx: context.Background(), reporter: func(_ context.Context, r diagnostics.Record) { level = r.Level }, detail: diagnostics.HTTPError{Status: 200}}
	_ = body.Close()
	if level != slog.LevelWarn {
		t.Fatal("successful close failure was not a warning")
	}
}

func TestControllerProxy_HTTPFailurePreservesBodyAndReportsOnce(t *testing.T) {
	body := `{"message":"provider unavailable"}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503); _, _ = w.Write([]byte(body)) }))
	defer upstream.Close()
	var records []diagnostics.Record
	proxy, err := NewControllerProxy(ProxyOptions{ControllerURL: upstream.URL, Reporter: func(_ context.Context, r diagnostics.Record) { records = append(records, r) }})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, httptest.NewRequest("GET", "http://panel.invalid/providers/proxies", nil))
	if response.Code != 503 || response.Body.String() != body {
		t.Fatal("proxy changed upstream response")
	}
	if len(records) != 1 {
		t.Fatalf("diagnostic count=%d", len(records))
	}
	var text string
	if raw, ok := records[0].Err.(*diagnostics.HTTPError); ok {
		text = raw.DiagnosticText()
	}
	if !strings.Contains(text, "503") || !strings.Contains(text, body) {
		t.Fatal("upstream diagnostic missing")
	}
}
