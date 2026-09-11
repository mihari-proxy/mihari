package daemon

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestRunStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	endpoint := transporttest.Endpoint(t)
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Endpoint: endpoint,
			Token:    "token",
			Version:  "dev",
			Ready:    ready,
		})
	}()

	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not become ready")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop")
	}
}

func TestRunSignalsReadyWithoutRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Endpoint: transporttest.Endpoint(t), Token: "token", Version: "dev", Ready: ready})
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not become ready")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunStartsAndStopsInjectedRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	runtimeStarted := make(chan struct{})
	runtimeStopped := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Endpoint: transporttest.Endpoint(t),
			Token:    "token",
			Version:  "dev",
			Ready:    ready,
			Runtime: runtimeFunc(func(ctx context.Context) error {
				close(runtimeStarted)
				<-ctx.Done()
				close(runtimeStopped)
				return nil
			}),
		})
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not become ready")
	}
	select {
	case <-runtimeStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not start")
	}
	cancel()
	select {
	case <-runtimeStopped:
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not stop")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunForwardsDiagnosticReporterToControlServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	reports := make(chan diagnostics.Record, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Listen:  func(context.Context) (net.Listener, error) { return listener, nil },
			Token:   "token",
			Version: "dev",
			Ready:   ready,
			DiagnosticReporter: func(_ context.Context, record diagnostics.Record) {
				reports <- record
			},
		})
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not become ready")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/v1/core", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d want=%d", response.StatusCode, http.StatusConflict)
	}
	select {
	case record := <-reports:
		if record.Level != slog.LevelError || record.Component != "control.server" {
			t.Fatalf("record=%#v", record)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control diagnostic was not reported")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type runtimeFunc func(context.Context) error

func (function runtimeFunc) Run(ctx context.Context) error { return function(ctx) }
