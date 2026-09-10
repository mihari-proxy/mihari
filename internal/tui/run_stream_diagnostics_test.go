package tui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestRun_StreamPayloadFailureUsesOwnedReporter(t *testing.T) {
	paths, err := platform.NewPaths(filepath.Join(t.TempDir(), "data")).Absolute()
	if err != nil {
		t.Fatal(err)
	}
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	// LoggingResources owns the normal close; this is an idempotent fallback.
	defer func() { _ = fs.Close() }()
	redactor := logging.NewRedactor("run-stream-secret")
	runtime, err := logging.Open(context.Background(), logging.RuntimeOptions{BasePath: paths.TUILog, Component: "tui", Config: logging.BootstrapConfig(), PrivateFS: fs, Redactor: redactor})
	if err != nil {
		t.Fatal(err)
	}
	// LoggingResources owns the normal close; this is an idempotent fallback.
	defer func() { _ = runtime.Close() }()
	probed := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/status" {
			_, _ = io.WriteString(w, `{"schema":"mihari/v1","revision":1}`)
			if calls.Add(1) == 2 {
				close(probed)
			}
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/v1/streams/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if strings.HasSuffix(r.URL.Path, "/logs") {
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"schema":"mihari/v1","stream":"logs","data":{"payload":"run-stream-secret","type":[]}}`))
		}
		_, _, _ = conn.Read(r.Context())
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Client: controlclient.NewHTTP(server.URL, "run-stream-secret", server.Client()), Input: strings.NewReader(""), Output: io.Discard, ErrorOutput: io.Discard, OpenLogging: func(context.Context) (LoggingResources, error) {
			return NewLoggingResources(runtime, redactor, fs), nil
		}})
	}()
	select {
	case <-probed:
	case <-ctx.Done():
		t.Fatal("session did not probe after payload failure")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not join")
	}
	raw, err := os.ReadFile(paths.TUILog)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["msg"] == "streams_failed" {
			count++
			if record["component"] != "tui.session" || record["level"] != "ERROR" {
				t.Fatalf("record=%v", record)
			}
		}
		if record["msg"] == "stream_failed" {
			t.Fatal("callback error diagnosed as client transport failure")
		}
	}
	if count != 1 {
		t.Fatalf("owned session diagnostics=%d want 1", count)
	}
	if strings.Contains(string(raw), "run-stream-secret") {
		t.Fatal("Run logger leaked stream payload")
	}
}
