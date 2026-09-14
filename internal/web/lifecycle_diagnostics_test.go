package web

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestNewControllerProxy_PreservesURLParseCause(t *testing.T) {
	_, err := NewControllerProxy(ProxyOptions{ControllerURL: "http://[fixture"})
	var parseErr *url.Error
	var api protocol.APIError
	if !errors.As(err, &parseErr) {
		t.Fatalf("URL parse cause lost: %v", err)
	}
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidArgument || err.Error() != "invalid controller url" {
		t.Fatalf("public error changed: %v", err)
	}
}

func TestServerServe_PreservesListenCause(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	})
	gateway, err := New(Options{Addr: listener.Addr().String(), ControllerURL: "http://127.0.0.1:9090"})
	if err != nil {
		t.Fatal(err)
	}
	err = gateway.Serve(context.Background())
	var opErr *net.OpError
	var api protocol.APIError
	if !errors.As(err, &opErr) {
		t.Fatalf("listen cause lost: %v", err)
	}
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState || err.Error() != "web gateway address is unavailable" {
		t.Fatalf("public error changed: %v", err)
	}
}

type failingPanelSource struct{ err error }

func (p failingPanelSource) ActiveDir() (string, error)       { return "", p.err }
func (failingPanelSource) SetupPath(string) string            { return "/" }
func (p failingPanelSource) PanelDir(string) (string, error)  { return "", p.err }
func (failingPanelSource) SetupPathFor(string, string) string { return "/" }

type fixedPanelSource struct{ root string }

func (p fixedPanelSource) ActiveDir() (string, error)       { return p.root, nil }
func (fixedPanelSource) SetupPath(string) string            { return "/" }
func (p fixedPanelSource) PanelDir(string) (string, error)  { return p.root, nil }
func (fixedPanelSource) SetupPathFor(string, string) string { return "/" }

func TestServeStatic_ReportsPanelSourceCause(t *testing.T) {
	cause := &os.PathError{Op: "open", Path: "/private/panel", Err: os.ErrPermission}
	var records []diagnostics.Record
	gateway, err := New(Options{Addr: "127.0.0.1:0", ControllerURL: "http://127.0.0.1:9090", Panel: failingPanelSource{err: cause}, Reporter: func(_ context.Context, record diagnostics.Record) { records = append(records, record) }})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/__mihari/panels/zashboard/"} {
		recorder := httptest.NewRecorder()
		gateway.handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	}
	if len(records) != 2 {
		t.Fatalf("static diagnostics=%d", len(records))
	}
	for _, record := range records {
		if record.Event != "static.failed" || !errors.Is(record.Err, cause) {
			t.Fatalf("static cause missing: %+v", record)
		}
	}
}

type failingStaticWriter struct {
	header http.Header
	err    error
}

func (w *failingStaticWriter) Header() http.Header       { return w.header }
func (*failingStaticWriter) WriteHeader(int)             {}
func (w *failingStaticWriter) Write([]byte) (int, error) { return 0, w.err }

func TestServePanelIndex_ReportsResponseWriteCause(t *testing.T) {
	dir := t.TempDir()
	path := dir + string(os.PathSeparator) + "index.html"
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("write static response failed")
	var records []diagnostics.Record
	gateway := &Server{Reporter: func(_ context.Context, record diagnostics.Record) { records = append(records, record) }}
	writer := &failingStaticWriter{header: make(http.Header), err: cause}
	gateway.servePanelIndex(writer, httptest.NewRequest(http.MethodGet, "http://panel.invalid/", nil), path, "")
	if len(records) != 1 || records[0].Event != "static.failed" || !errors.Is(records[0].Err, cause) {
		t.Fatalf("write cause missing: %+v", records)
	}
	if strings.Contains(records[0].Err.Error(), "fixture") {
		t.Fatal("response content entered error text")
	}
}

func TestServeStatic_ReportsNonNotExistStatCause(t *testing.T) {
	root := t.TempDir()
	cause := &os.PathError{Op: "stat", Path: root + string(os.PathSeparator) + "index.html", Err: os.ErrPermission}
	var records []diagnostics.Record
	gateway := &Server{Panel: fixedPanelSource{root: root}, Reporter: func(_ context.Context, record diagnostics.Record) { records = append(records, record) }}
	gateway.statPath = func(name string) (fs.FileInfo, error) {
		if filepath.Base(name) == "dist" {
			return nil, fs.ErrNotExist
		}
		return nil, cause
	}
	gateway.serveStatic(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://panel.invalid/", nil))
	if len(records) != 1 || records[0].Event != "static.failed" || !errors.Is(records[0].Err, cause) {
		t.Fatalf("non-not-exist stat cause missing: %+v", records)
	}
}

func TestServeStatic_ReportsFileResponseWriteCause(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+string(os.PathSeparator)+"asset.js", []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("write static file response failed")
	var records []diagnostics.Record
	gateway := &Server{Panel: fixedPanelSource{root: root}, Reporter: func(_ context.Context, record diagnostics.Record) { records = append(records, record) }}
	writer := &failingStaticWriter{header: make(http.Header), err: cause}
	gateway.serveStatic(writer, httptest.NewRequest(http.MethodGet, "http://panel.invalid/asset.js", nil))
	if len(records) != 1 || records[0].Event != "static.failed" || !errors.Is(records[0].Err, cause) {
		t.Fatalf("file response write cause missing: %+v", records)
	}
}

type blockingPanelSource struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockingPanelSource) ActiveDir() (string, error) {
	p.once.Do(func() { close(p.entered) })
	<-p.release
	return "", nil
}
func (*blockingPanelSource) SetupPath(string) string            { return "/" }
func (*blockingPanelSource) PanelDir(string) (string, error)    { return "", nil }
func (*blockingPanelSource) SetupPathFor(string, string) string { return "/" }

func TestServerServe_PreservesShutdownFailure(t *testing.T) {
	panel := &blockingPanelSource{entered: make(chan struct{}), release: make(chan struct{})}
	gateway, err := New(Options{Addr: "127.0.0.1:0", ControllerURL: "http://127.0.0.1:9090", Panel: panel})
	if err != nil {
		t.Fatal(err)
	}
	gateway.shutdown = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- gateway.Serve(ctx) }()
	addr := waitListenAddr(gateway.ListenAddr, time.Second)
	if addr == "" {
		t.Fatal("gateway did not listen")
	}
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		_, _ = http.Get("http://" + addr + "/")
	}()
	select {
	case <-panel.entered:
	case <-time.After(time.Second):
		close(panel.release)
		t.Fatal("request did not enter blocking handler")
	}
	cancel()
	err = <-serveDone
	close(panel.release)
	<-requestDone
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown cause lost: %v", err)
	}
}
