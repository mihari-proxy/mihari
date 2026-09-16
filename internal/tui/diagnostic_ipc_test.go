package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	controlserver "github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type diagnosticRulesRuntime struct {
	controlserver.RuntimeAPI
	upstream *mihomo.Client
}

func (r diagnosticRulesRuntime) Rules(ctx context.Context) (mihomo.Rules, error) {
	return r.upstream.Rules(ctx)
}

func TestDiagnosticIPC_UpstreamFailureReachesF2AndOriginalCopy(t *testing.T) {
	const original = "token=fixture-original\x1b[31m\nconfiguration: password=synthetic"
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := io.WriteString(w, original); err != nil {
			t.Error(err)
		}
	}))
	defer upstream.Close()
	history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{InstanceID: "ipc-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := transporttest.Endpoint(t)
	listener, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := controlserver.New(controlserver.Options{Token: "fixture-control", Store: state.NewStore(state.Snapshot{}), Runtime: diagnosticRulesRuntime{upstream: mihomo.NewClient(upstream.URL, "fixture-controller", upstream.Client())}, DiagnosticHistory: history, DiagnosticReporter: diagnostics.NewOwner(history, nil).Report})
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("IPC shutdown timed out")
		}
	})
	ipcTransport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return transport.DialContext(ctx, endpoint) }}
	client := controlclient.NewHTTP("http://mihari", "fixture-control", &http.Client{Transport: ipcTransport, Timeout: 10 * time.Second})
	t.Cleanup(ipcTransport.CloseIdleConnections)
	model := newModelWithClientContext(ctx, nil, client)
	model.active = ui.PageRules
	model.width, model.height = 100, 28
	_, load := model.pages[ui.PageRules].Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if load == nil {
		t.Fatal("missing real Rules command")
	}
	next, _ := model.Update(load())
	model = next.(Model)
	if len(model.diagnosticWindow.entries) != 1 {
		t.Fatalf("occurrences=%d", len(model.diagnosticWindow.entries))
	}
	next, fetch := model.Update(diagnosticKey(tea.KeyF2))
	model = next.(Model)
	if fetch != nil {
		next, _ = model.Update(fetch())
		model = next.(Model)
	}
	snapshot := model.diagnosticWindow.pinned
	if snapshot.ID == "" || !strings.Contains(snapshot.Detail, original) {
		t.Fatalf("original owner detail lost: %+v", snapshot)
	}
	if !strings.Contains(strings.Join(model.diagnosticWindow.detailLines(100), ""), `token=fixture-original\x1b[31m`) {
		t.Fatalf("F2 did not escape original terminal control: %q", model.diagnosticWindow.detailLines(100))
	}
	copied := ""
	model.diagnosticWindow.copyText = func(value string) error { copied = value; return nil }
	_, copyCmd := model.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if copyCmd == nil {
		t.Fatal("missing copy command")
	}
	copyCmd()
	if copied != snapshot.Detail || !strings.Contains(copied, original) {
		t.Fatal("copy lost original bytes")
	}
	if calls.Load() != 1 {
		t.Fatalf("diagnostic delivery repeated upstream request: %d", calls.Load())
	}
	page := history.List("", 0, 100)
	if len(page.Records) != 1 || page.Records[0].ID != snapshot.ID {
		t.Fatal("transport created a second occurrence")
	}
}
