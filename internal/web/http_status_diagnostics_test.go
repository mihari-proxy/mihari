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
	"time"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type partialDiagnosticBody struct {
	data              []byte
	readErr, closeErr error
	reads, closes     int
}

func TestHTTPBody_CloseOwnerCancellationIsInfo(t *testing.T) {
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			if errors.Is(failure, context.DeadlineExceeded) {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			cancel()
			source := &partialDiagnosticBody{closeErr: failure}
			reports := 0
			body := &observedHTTPBody{ReadCloser: source, ctx: ctx, reporter: func(_ context.Context, record diagnostics.Record) {
				reports++
				if record.Level != slog.LevelInfo || !errors.Is(record.Err, failure) {
					t.Error("cancellation lost INFO or original cause")
				}
			}, detail: diagnostics.HTTPError{Status: http.StatusOK}}
			if err := body.Close(); !errors.Is(err, failure) {
				t.Fatal("close result changed")
			}
			if reports != 1 || source.closes != 1 {
				t.Fatal("owner cancellation missing or body not closed exactly once")
			}
		})
	}
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
	original := bytes.Repeat([]byte{0x1f, 0x8b, 'a'}, 100000)
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
	if source.closes != 1 || len(records) != 1 || len(body.raw) > diagnostics.MaxHTTPBodyBytes+1 {
		t.Fatal("unbounded capture or duplicate close/report")
	}
	detail := records[0].Err.(*diagnostics.HTTPError)
	if len(detail.Body) > diagnostics.MaxHTTPBodyBytes || !detail.BodyTruncated || !strings.Contains(detail.DiagnosticText(), "truncated") || !errors.Is(detail, io.ErrUnexpectedEOF) || !strings.Contains(detail.DiagnosticText(), "close failed") {
		t.Fatal("partial body lost diagnostic causes")
	}
}

func TestHTTPBody_SuccessNoDumpAndCanceledReadInfo(t *testing.T) {
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
		want := 0
		if cancelled {
			want = 1
		}
		if len(records) != want || len(body.raw) != 0 {
			t.Fatal("incorrect cancellation reporting or successful body dumped")
		}
		if cancelled && (records[0].Level != slog.LevelInfo || !errors.Is(records[0].Err, context.Canceled)) {
			t.Fatal("cancellation lost INFO classification or cause")
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
