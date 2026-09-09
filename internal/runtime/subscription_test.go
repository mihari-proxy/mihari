package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type reloadController struct {
	fakeController
	mu        sync.Mutex
	reloads   int
	reloadErr error
	reload    func(context.Context) error
}

func (c *reloadController) Reload(ctx context.Context, _ string, _ bool) error {
	c.mu.Lock()
	c.reloads++
	reload, reloadErr := c.reload, c.reloadErr
	c.mu.Unlock()
	if reload != nil {
		return reload(ctx)
	}
	return reloadErr
}

func subscriptionManager(t *testing.T, handler http.Handler) (*Manager, *subscription.Service, *reloadController, string) {
	return subscriptionManagerWithDownloader(t, handler, nil)
}

func subscriptionManagerWithDownloader(t *testing.T, handler http.Handler, downloader subscription.Fetcher) (*Manager, *subscription.Service, *reloadController, string) {
	t.Helper()
	root := t.TempDir()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	if downloader == nil {
		downloader = subscription.NewDownloader(subscription.DownloaderOptions{Client: server.Client()})
	}
	service, err := subscription.Open(subscription.ServiceOptions{
		CatalogPath: filepath.Join(root, "subscriptions", "catalog.yaml"),
		CacheDir:    filepath.Join(root, "subscriptions", "cache"),
		Downloader:  downloader,
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	runtimeConfig := filepath.Join(root, "runtime", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(runtimeConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimeConfig, []byte("proxies: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	controller := &reloadController{}
	manager := newTestManager(Options{
		Subscriptions: service,
		Settings:      settings,
		RuntimeConfig: runtimeConfig,
		StagingDir:    filepath.Join(root, "staging"),
		ValidateConfig: func(context.Context, string) error {
			return nil
		},
		Controller: controller,
	})
	return manager, service, controller, server.URL
}

func TestSubscriptionRefreshAndOfflineSwitch(t *testing.T) {
	manager, _, controller, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		name := request.URL.Query().Get("name")
		_, _ = writer.Write([]byte("proxies:\n  - {name: " + name + "}\n"))
	}))
	a, err := manager.AddSubscription(context.Background(), Operation{ID: "add-a", Source: "test"}, AddSubscriptionInput{Name: "A", URL: url + "?name=a"})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Cached {
		t.Fatalf("add should fetch immediately: %#v", a)
	}
	b, err := manager.AddSubscription(context.Background(), Operation{ID: "add-b", Source: "test"}, AddSubscriptionInput{Name: "B", URL: url + "?name=b"})
	if err != nil {
		t.Fatal(err)
	}
	if !b.Cached {
		t.Fatalf("add should fetch immediately: %#v", b)
	}
	// First successful fetch becomes active; switch offline to B.
	if _, err := manager.UseSubscription(context.Background(), Operation{ID: "use-b", Source: "test"}, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := manager.Subscriptions().ActiveID; got != b.ID {
		t.Fatalf("active=%q", got)
	}
	controller.mu.Lock()
	reloads := controller.reloads
	controller.mu.Unlock()
	if reloads < 2 {
		t.Fatalf("reloads=%d", reloads)
	}
}

func TestSubscriptionDiagnostic_RefreshSuccessUsesExecutionOperationJSON(t *testing.T) {
	const secret = "subscription-secret"
	manager, _, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\nrules: [MATCH,DIRECT]\n"))
	}))
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "seed", Source: "test"}, AddSubscriptionInput{Name: "Main", URL: url + "?token=" + secret})
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	manager.diagnosticReporter = logging.NewDiagnosticReporter(
		slog.New(logging.NewJSONHandler(&output, level, "daemon", logging.NewRedactor(secret))),
		logging.NewRedactor(secret),
	)
	if _, err := manager.RefreshSubscription(context.Background(), Operation{ID: "refresh-success", Source: "test"}, profile.ID); err != nil {
		t.Fatal(err)
	}

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("diagnostic JSON: %v; output=%s", err, output.String())
	}
	if record["msg"] != "operation.succeeded" || record["operation_id"] != "refresh-success" || record["operation"] != "subscription.refresh" {
		t.Fatalf("diagnostic record=%#v", record)
	}
	if strings.Contains(output.String(), secret) || strings.Contains(output.String(), url) {
		t.Fatalf("diagnostic leaked subscription input: %s", output.String())
	}
}

func TestSubscriptionDiagnostic_UseCacheFailurePreservesCauseAndSafeJSON(t *testing.T) {
	const secret = "subscription-secret"
	manager, service, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\nrules: [MATCH,DIRECT]\n"))
	}))
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "seed", Source: "test"}, AddSubscriptionInput{Name: "Main", URL: url + "?token=" + secret})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(service.CachePath(profile.ID)); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	redactor := logging.NewRedactor(secret)
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	manager.diagnosticReporter = logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, level, "daemon", redactor)), redactor)
	_, err = manager.UseSubscription(context.Background(), Operation{ID: "use-cache", Source: "test"}, profile.ID)
	var api protocol.APIError
	if !errors.Is(err, os.ErrNotExist) || !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || api.Message != "subscription cache is unavailable" {
		t.Fatalf("use cache error=%v", err)
	}

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("diagnostic JSON: %v; output=%s", err, output.String())
	}
	if record["msg"] != "operation.failed" || record["operation_id"] != "use-cache" || record["operation"] != "subscription.use" {
		t.Fatalf("diagnostic record=%#v", record)
	}
	cause, _ := record["cause"].(string)
	if !strings.Contains(cause, "path operation open") || strings.Contains(output.String(), secret) || strings.Contains(output.String(), url) {
		t.Fatalf("cause=%q output=%s", cause, output.String())
	}
}

func TestSubscriptionDiagnostic_UnknownCauseUsesConservativeJSONSummary(t *testing.T) {
	const opaque = "subscription-private-unknown-error"
	var output bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	redactor := logging.NewRedactor()
	reporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, level, "daemon", redactor)), redactor)
	reporter(logging.WithOperation(context.Background(), logging.OperationMetadata{ID: "unknown-cause", Name: "subscription.refresh"}), diagnostics.Record{
		Component: "runtime", Event: "operation.failed", Level: slog.LevelError, Err: errors.New(opaque),
	})

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("diagnostic JSON: %v; output=%s", err, output.String())
	}
	if record["operation_id"] != "unknown-cause" || record["operation"] != "subscription.refresh" || record["cause"] != "error (*errors.errorString)" || strings.Contains(output.String(), opaque) {
		t.Fatalf("diagnostic record=%#v output=%s", record, output.String())
	}
}

type scriptedSubscriptionFetch struct {
	result subscription.FetchResult
	err    error
}

type scriptedSubscriptionFetcher struct {
	mu      sync.Mutex
	entries []scriptedSubscriptionFetch
	calls   int
}

func (f *scriptedSubscriptionFetcher) Fetch(context.Context, subscription.FetchRequest) (subscription.FetchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls >= len(f.entries) {
		return subscription.FetchResult{}, errors.New("unexpected subscription fetch")
	}
	entry := f.entries[f.calls]
	f.calls++
	return entry.result, entry.err
}

func (f *scriptedSubscriptionFetcher) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestSubscriptionDiagnostic_FailedReplayAndNewIDKeepCausesIsolated(t *testing.T) {
	firstCause := &os.PathError{Op: "read", Path: "/private/replay-cause", Err: os.ErrPermission}
	secondCause := &os.PathError{Op: "open", Path: "/private/new-id-cause", Err: os.ErrNotExist}
	fetcher := &scriptedSubscriptionFetcher{entries: []scriptedSubscriptionFetch{
		{result: subscription.FetchResult{Content: []byte("proxies: []\n")}},
		{err: firstCause},
		{err: secondCause},
	}}
	manager, _, _, _ := subscriptionManagerWithDownloader(t, http.NotFoundHandler(), fetcher)
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "seed", Source: "test"}, AddSubscriptionInput{Name: "Main", URL: "https://example.test/sub"})
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	redactor := logging.NewRedactor("replay-cause", "new-id-cause")
	manager.diagnosticReporter = logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, level, "daemon", redactor)), redactor)
	_, firstErr := manager.RefreshSubscription(context.Background(), Operation{ID: "replay", Source: "test"}, profile.ID)
	_, replayErr := manager.RefreshSubscription(context.Background(), Operation{ID: "replay", Source: "test"}, profile.ID)
	_, secondErr := manager.RefreshSubscription(context.Background(), Operation{ID: "new-id", Source: "test"}, profile.ID)
	if !errors.Is(firstErr, firstCause) || !errors.Is(replayErr, firstCause) || !errors.Is(secondErr, secondCause) {
		t.Fatalf("refresh causes crossed or were lost: first=%v replay=%v second=%v", firstErr, replayErr, secondErr)
	}
	if fetcher.CallCount() != 3 { // Add, replay owner, and distinct new-ID owner.
		t.Fatalf("fetches=%d want 3", fetcher.CallCount())
	}

	raw := output.String()
	decoder := json.NewDecoder(strings.NewReader(raw))
	var records []map[string]any
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if len(records) != 2 {
		t.Fatalf("error records=%#v", records)
	}
	for index, want := range []struct{ id, cause string }{{"replay", "path operation read: permission denied"}, {"new-id", "path operation open: file does not exist"}} {
		record := records[index]
		if record["msg"] != "operation.failed" || record["level"] != "ERROR" || record["operation_id"] != want.id || record["operation"] != "subscription.refresh" || record["cause"] != want.cause {
			t.Fatalf("record[%d]=%#v", index, record)
		}
	}
	if strings.Contains(raw, "replay-cause") || strings.Contains(raw, "new-id-cause") {
		t.Fatalf("diagnostic exposed private causes: %s", raw)
	}
}

func TestSubscriptionDiagnostic_AddFailedChildKeepsSafeLastErrorAndCause(t *testing.T) {
	const private = "private-add-fetch-sentinel"
	cause := &os.PathError{Op: "read", Path: "/private/" + private, Err: os.ErrPermission}
	fetcher := &scriptedSubscriptionFetcher{entries: []scriptedSubscriptionFetch{{err: cause}}}
	manager, _, _, _ := subscriptionManagerWithDownloader(t, http.NotFoundHandler(), fetcher)

	var output bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	redactor := logging.NewRedactor(private)
	manager.diagnosticReporter = logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, level, "daemon", redactor)), redactor)
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "add", Source: "test"}, AddSubscriptionInput{Name: "Broken", URL: "https://example.test/sub"})
	if err != nil {
		t.Fatalf("registration should survive child failure: %v", err)
	}
	if profile.Cached || profile.LastError != "subscription refresh failed" {
		t.Fatalf("unsafe or unexpected add response: %#v", profile)
	}
	catalog, err := json.Marshal(manager.Subscriptions())
	if err != nil || strings.Contains(string(catalog), private) {
		t.Fatalf("catalog response leaked private cause: %s err=%v", catalog, err)
	}

	raw := output.String()
	decoder := json.NewDecoder(strings.NewReader(raw))
	var records []map[string]any
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if len(records) != 2 {
		t.Fatalf("records=%#v", records)
	}
	child := records[1]
	if child["msg"] != "operation.failed" || child["level"] != "ERROR" || child["operation_id"] != "add-fetch" || child["operation"] != "subscription.refresh" || child["cause"] != "path operation read: permission denied" || strings.Contains(raw, private) {
		t.Fatalf("unsafe child diagnostic=%#v output=%s", child, raw)
	}
}

func TestSubscriptionDiagnostic_ReplayAndConcurrentIDsRecordOnlyExecutionOwners(t *testing.T) {
	manager, _, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\nrules: [MATCH,DIRECT]\n"))
	}))
	first, err := manager.AddSubscription(context.Background(), Operation{ID: "seed-first", Source: "test"}, AddSubscriptionInput{Name: "First", URL: url + "?name=first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.AddSubscription(context.Background(), Operation{ID: "seed-second", Source: "test"}, AddSubscriptionInput{Name: "Second", URL: url + "?name=second"})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	counts := make(map[string]int)
	manager.diagnosticReporter = func(ctx context.Context, record diagnostics.Record) {
		if record.Event != "operation.succeeded" {
			return
		}
		operation, _ := logging.OperationFromContext(ctx)
		if operation.Name != "subscription.refresh" {
			t.Errorf("operation=%#v", operation)
			return
		}
		mu.Lock()
		counts[operation.ID]++
		mu.Unlock()
	}
	if _, err := manager.RefreshSubscription(context.Background(), Operation{ID: "replay", Source: "test"}, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RefreshSubscription(context.Background(), Operation{ID: "replay", Source: "test"}, first.ID); err != nil {
		t.Fatal(err)
	}

	errs := make(chan error, 2)
	for _, request := range []struct{ id, operationID string }{{first.ID, "concurrent-one"}, {second.ID, "concurrent-two"}} {
		request := request
		go func() {
			_, err := manager.RefreshSubscription(context.Background(), Operation{ID: request.operationID, Source: "test"}, request.id)
			errs <- err
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, operationID := range []string{"replay", "concurrent-one", "concurrent-two"} {
		if counts[operationID] != 1 {
			t.Fatalf("counts=%#v", counts)
		}
	}
}

func TestSubscriptionDiagnostic_AutoFallbackRecoveryDoesNotReportFailure(t *testing.T) {
	deadProxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadProxy.Close()
	proxyURL, err := url.Parse(deadProxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	downloader := subscription.NewDownloader(subscription.DownloaderOptions{Client: http.DefaultClient, ProxyURL: proxyURL})
	manager, _, _, serverURL := subscriptionManagerWithDownloader(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\nrules: [MATCH,DIRECT]\n"))
	}), downloader)
	var mu sync.Mutex
	var records []struct {
		operation logging.OperationMetadata
		record    diagnostics.Record
	}
	manager.diagnosticReporter = func(ctx context.Context, record diagnostics.Record) {
		operation, _ := logging.OperationFromContext(ctx)
		mu.Lock()
		records = append(records, struct {
			operation logging.OperationMetadata
			record    diagnostics.Record
		}{operation: operation, record: record})
		mu.Unlock()
	}
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "auto-add", Source: "test"}, AddSubscriptionInput{Name: "Auto", URL: serverURL, ProxyMode: subscription.ProxyModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	if !profile.Cached {
		t.Fatalf("fallback recovery did not cache profile: %#v", profile)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(records) != 2 {
		t.Fatalf("records=%#v", records)
	}
	wantOperations := []logging.OperationMetadata{{ID: "auto-add", Name: "subscription.add"}, {ID: "auto-add-fetch", Name: "subscription.refresh"}}
	for index, diagnostic := range records {
		if diagnostic.operation != wantOperations[index] || diagnostic.record.Event != "operation.succeeded" || diagnostic.record.Level != slog.LevelInfo {
			t.Fatalf("recovered fallback recorded a failure: %#v", records)
		}
	}
}

func TestAddSubscriptionFetchesImmediately(t *testing.T) {
	var fetches atomic.Int32
	manager, _, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		_, _ = writer.Write([]byte("proxies:\n  - {name: node-a, type: direct}\n"))
	}))
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "add", Source: "test"}, AddSubscriptionInput{Name: "Main", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	if fetches.Load() != 1 {
		t.Fatalf("fetches=%d want 1", fetches.Load())
	}
	if !profile.Cached || profile.Generation == 0 || profile.UpdatedAt.IsZero() {
		t.Fatalf("profile not cached after add: %#v", profile)
	}
	if got := manager.Subscriptions().ActiveID; got != profile.ID {
		t.Fatalf("first fetch should become active: %q", got)
	}
}

func TestAddSubscriptionKeepsProfileWhenFetchFails(t *testing.T) {
	manager, _, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	var records []struct {
		operation logging.OperationMetadata
		record    diagnostics.Record
	}
	manager.diagnosticReporter = func(ctx context.Context, record diagnostics.Record) {
		operation, _ := logging.OperationFromContext(ctx)
		records = append(records, struct {
			operation logging.OperationMetadata
			record    diagnostics.Record
		}{operation: operation, record: record})
	}
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "add", Source: "test"}, AddSubscriptionInput{Name: "Broken", URL: url})
	if err != nil {
		t.Fatalf("add should keep registration when fetch fails: %v", err)
	}
	if profile.Cached || profile.LastError == "" {
		t.Fatalf("expected uncached profile with last_error: %#v", profile)
	}
	if len(manager.Subscriptions().Profiles) != 1 {
		t.Fatalf("profiles=%#v", manager.Subscriptions().Profiles)
	}
	if len(records) != 2 || records[0].record.Event != "operation.succeeded" || records[0].operation != (logging.OperationMetadata{ID: "add", Name: "subscription.add"}) || records[1].record.Event != "operation.failed" || records[1].operation != (logging.OperationMetadata{ID: "add-fetch", Name: "subscription.refresh"}) {
		t.Fatalf("diagnostics=%#v", records)
	}
}

func TestRefreshCannotRecreateSubscriptionDeletedDuringDownload(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	manager, _, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			// Auto-fetch on add completes immediately.
			_, _ = writer.Write([]byte("proxies: []\n"))
			return
		}
		close(entered)
		<-release
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "add", Source: "test"}, AddSubscriptionInput{Name: "A", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	var diagnosticsMu sync.Mutex
	var records []struct {
		operation logging.OperationMetadata
		record    diagnostics.Record
	}
	manager.diagnosticReporter = func(ctx context.Context, record diagnostics.Record) {
		operation, _ := logging.OperationFromContext(ctx)
		diagnosticsMu.Lock()
		records = append(records, struct {
			operation logging.OperationMetadata
			record    diagnostics.Record
		}{operation: operation, record: record})
		diagnosticsMu.Unlock()
	}
	done := make(chan error, 1)
	go func() {
		_, err := manager.RefreshSubscription(context.Background(), Operation{ID: "refresh", Source: "test"}, profile.ID)
		done <- err
	}()
	<-entered
	if err := manager.RemoveSubscription(context.Background(), Operation{ID: "remove", Source: "test"}, profile.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	err = <-done
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("refresh error=%v", err)
	}
	if len(manager.Subscriptions().Profiles) != 0 {
		t.Fatal("deleted subscription was recreated")
	}
	diagnosticsMu.Lock()
	defer diagnosticsMu.Unlock()
	if len(records) != 2 || records[0].operation != (logging.OperationMetadata{ID: "remove", Name: "subscription.remove"}) || records[0].record.Event != "operation.succeeded" || records[1].operation != (logging.OperationMetadata{ID: "refresh", Name: "subscription.refresh"}) || records[1].record.Event != "operation.failed" || records[1].record.Level != slog.LevelDebug {
		t.Fatalf("diagnostics=%#v", records)
	}
}

func TestReloadFailureRollsBackSubscriptionActivation(t *testing.T) {
	manager, _, controller, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	// Force the auto-fetch that runs inside Add to fail at mihomo reload.
	first, second := errors.New("initial reload failure"), errors.New("rollback reload failure")
	calls := 0
	controller.reload = func(context.Context) error {
		calls++
		if calls == 1 {
			return first
		}
		return second
	}
	recorder := &coreDiagnosticRecorder{}
	manager.diagnosticReporter = recorder.report
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "add", Source: "test"}, AddSubscriptionInput{Name: "A", URL: url})
	if err != nil {
		t.Fatalf("add should not fail hard when auto-fetch reloads fail: %v", err)
	}
	records := recorder.snapshot()
	if len(records) != 2 || records[0].record.Event != "operation.succeeded" || records[1].operation.ID != "add-fetch" || records[1].record.Event != "operation.failed" || !errors.Is(records[1].record.Err, first) || !errors.Is(records[1].record.Err, second) {
		t.Errorf("auto-fetch lost cause or changed parent outcome: %#v", records)
	}
	got := manager.Subscriptions()
	if got.ActiveID != "" || got.Profiles[0].Generation != 0 || profile.Cached {
		t.Fatalf("failed auto-fetch should roll back cache/activation: profile=%#v catalog=%#v", profile, got)
	}
	if snapshot := manager.Snapshot(); snapshot.Health != "degraded" || snapshot.Config.DesiredRevision <= snapshot.Config.ObservedRevision {
		t.Fatalf("rollback failure was not published as degraded: %#v", snapshot)
	}
}

func TestProxySelectionWaitsForSubscriptionReload(t *testing.T) {
	manager, _, controller, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	// Add auto-fetches once; only block reload on the subsequent explicit refresh.
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "add", Source: "test"}, AddSubscriptionInput{Name: "A", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	reloadEntered := make(chan struct{})
	releaseReload := make(chan struct{})
	selected := make(chan struct{})
	controller.reload = func(context.Context) error {
		close(reloadEntered)
		<-releaseReload
		return nil
	}
	controller.selectProxy = func(context.Context, string, string) error {
		close(selected)
		return nil
	}
	refreshDone := make(chan error, 1)
	go func() {
		_, err := manager.RefreshSubscription(context.Background(), Operation{ID: "refresh", Source: "test"}, profile.ID)
		refreshDone <- err
	}()
	<-reloadEntered
	selectDone := make(chan error, 1)
	go func() {
		selectDone <- manager.SelectProxy(context.Background(), Operation{ID: "select", Source: "test"}, "GLOBAL", "DIRECT")
	}()
	select {
	case <-selected:
		t.Fatal("proxy selection overlapped subscription reload")
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseReload)
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	if err := <-selectDone; err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionAddPersistsProxyMode(t *testing.T) {
	manager, _, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\nrules: [MATCH,DIRECT]\n"))
	}))
	profile, err := manager.AddSubscription(context.Background(), Operation{ID: "add", Source: "test"}, AddSubscriptionInput{Name: "A", URL: url, ProxyMode: subscription.ProxyModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	if profile.ProxyMode != subscription.ProxyModeAuto {
		t.Fatalf("ProxyMode=%q want %q", profile.ProxyMode, subscription.ProxyModeAuto)
	}
}

func TestSubscriptionSetUpdatesProxyMode(t *testing.T) {
	manager, _, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\nrules: [MATCH,DIRECT]\n"))
	}))
	added, err := manager.AddSubscription(context.Background(), Operation{ID: "add", Source: "test"}, AddSubscriptionInput{Name: "A", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	mode := subscription.ProxyModeProxy
	updated, err := manager.SetSubscription(context.Background(), Operation{ID: "set", Source: "test"}, added.ID, SetSubscriptionInput{ProxyMode: &mode})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProxyMode != subscription.ProxyModeProxy {
		t.Fatalf("ProxyMode=%q want %q", updated.ProxyMode, subscription.ProxyModeProxy)
	}
}

func TestLogging_RefreshSecretsKeepsToken(t *testing.T) {
	manager, _, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\nrules: [MATCH,DIRECT]\n"))
	}))
	controlToken := "control-token-that-must-remain-redacted"
	baseSecrets := []string{controlToken}
	redactor := logging.NewRedactor()
	var snapshots [][]string
	manager.refreshLogSecrets = func(catalogURLs []string) {
		copiedURLs := append([]string{}, catalogURLs...)
		snapshots = append(snapshots, copiedURLs)
		redactor.ReplaceExact(append(append([]string{}, baseSecrets...), catalogURLs...))
	}

	firstURL := url + "?token=first-subscription-secret"
	added, err := manager.AddSubscription(context.Background(), Operation{ID: "logging-add", Source: "test"}, AddSubscriptionInput{Name: "Main", URL: firstURL})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || !reflect.DeepEqual(snapshots[0], []string{firstURL}) {
		t.Fatalf("secret snapshots after add: count=%d", len(snapshots))
	}
	if got := redactor.String("control=" + controlToken); got != "control=***" {
		t.Fatal("control token lost after add refresh")
	}

	secondURL := url + "?token=second-subscription-secret"
	if _, err := manager.SetSubscription(context.Background(), Operation{ID: "logging-set", Source: "test"}, added.ID, SetSubscriptionInput{URL: &secondURL}); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 || !reflect.DeepEqual(snapshots[1], []string{secondURL}) {
		t.Fatalf("secret snapshots after set: count=%d", len(snapshots))
	}
	if got := redactor.String("control=" + controlToken); got != "control=***" {
		t.Fatal("control token lost after set refresh")
	}

	if err := manager.RemoveSubscription(context.Background(), Operation{ID: "logging-remove", Source: "test"}, added.ID); err != nil {
		t.Fatal(err)
	}
	finalURLs := -1
	if len(snapshots) > 0 {
		finalURLs = len(snapshots[len(snapshots)-1])
	}
	if len(snapshots) != 3 || finalURLs != 0 {
		t.Fatalf("secret snapshots after remove: count=%d final_urls=%d", len(snapshots), finalURLs)
	}
	if got := redactor.String("control=" + controlToken); got != "control=***" {
		t.Fatal("control token lost after remove refresh")
	}
}

func TestSubscriptionSetRestoreFailureRefreshesSecretsAndDegrades(t *testing.T) {
	manager, service, _, serverURL := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if _, err := writer.Write([]byte("proxies: []\nrules: [MATCH,DIRECT]\n")); err != nil {
			t.Errorf("write fixture response: %v", err)
		}
	}))
	oldURL := serverURL + "?token=old-subscription-secret"
	added, err := manager.AddSubscription(context.Background(), Operation{ID: "restore-fail-add", Source: "test"}, AddSubscriptionInput{Name: "Main", URL: oldURL})
	if err != nil {
		t.Fatal(err)
	}
	beforeRevision := manager.Snapshot().Revision
	controlToken := "control-token-that-must-remain-redacted"
	redactor := logging.NewRedactor()
	var snapshots [][]string
	manager.refreshLogSecrets = func(catalogURLs []string) {
		copiedURLs := append([]string(nil), catalogURLs...)
		snapshots = append(snapshots, copiedURLs)
		redactor.ReplaceExact(append([]string{controlToken}, catalogURLs...))
	}
	manager.refreshSubscriptionLogSecrets()
	if len(snapshots) != 1 || len(snapshots[0]) != 1 || snapshots[0][0] != oldURL {
		t.Fatalf("initial secret snapshot count=%d", len(snapshots))
	}
	snapshots = nil

	catalogPath := filepath.Join(filepath.Dir(filepath.Dir(service.CachePath(added.ID))), "catalog.yaml")
	manager.validateConfig = func(context.Context, string) error {
		if err := os.Remove(catalogPath); err != nil {
			return err
		}
		if err := os.Mkdir(catalogPath, 0o700); err != nil {
			return err
		}
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "reject generated configuration"}
	}
	newURL := serverURL + "?token=new-subscription-secret"
	op := Operation{ID: "restore-fail-set", Source: "test"}
	_, err = manager.SetSubscription(context.Background(), op, added.ID, SetSubscriptionInput{URL: &newURL})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure || apiError.Message != "subscription state rollback failed" {
		t.Fatalf("err code=%q message=%q", apiError.Code, apiError.Message)
	}
	if strings.Contains(err.Error(), "subscription-secret") || strings.Contains(err.Error(), "token=") {
		t.Fatal("restore failure exposed a subscription secret")
	}
	current := service.Snapshot()
	index := current.Index(added.ID)
	if index < 0 || current.Profiles[index].URL != newURL {
		t.Fatal("failed restore did not leave the actual catalog mutation observable")
	}
	if len(snapshots) != 1 || len(snapshots[0]) != 1 || snapshots[0][0] != newURL {
		t.Fatalf("refreshed secret snapshot count=%d", len(snapshots))
	}
	if got := redactor.String("request=" + newURL); got != "request=***" {
		t.Fatal("new subscription URL was not redacted")
	}
	snapshot := manager.Snapshot()
	if snapshot.Revision != beforeRevision+1 || snapshot.Health != "degraded" || snapshot.Config.Status != "degraded" || snapshot.Config.LastError != "generated configuration rollback could not be confirmed" {
		t.Fatalf("revision=%d health=%q config_status=%q config_error=%q", snapshot.Revision, snapshot.Health, snapshot.Config.Status, snapshot.Config.LastError)
	}
	if strings.Contains(snapshot.LastError, "subscription-secret") || strings.Contains(snapshot.Config.LastError, "subscription-secret") {
		t.Fatal("degraded state exposed a subscription secret")
	}
}

func TestBootstrapGeneration_IgnoresLegacyTunSettingsFields(t *testing.T) {
	manager, _, _, _ := subscriptionManager(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("bootstrap fetched subscription") }))
	settings := manager.settingsSnapshot()
	settings.Tun = map[string]any{"enable": false, "stack": "legacy", "device": "legacy-device", "x-extra": true}
	before := settings.Clone()
	candidate, err := manager.prepareCatalogConfigWithSettings(context.Background(), subscription.Defaults(), settings, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.cleanup()
	document, err := subscription.ParseDocument(candidate.content)
	if err != nil {
		t.Fatal(err)
	}
	tun, ok := document["tun"].(subscription.Document)
	if !ok || len(tun) != 1 || tun["enable"] != false {
		t.Fatalf("bootstrap synthesized legacy TUN fields: %#v", tun)
	}
	if !reflect.DeepEqual(settings, before) {
		t.Fatal("bootstrap mutated settings input")
	}
}
