package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/geoip"
	"github.com/mihari-proxy/mihari/internal/logging"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type schedulerController struct{ runtimeapi.Controller }

func (schedulerController) Reload(context.Context, string, bool) error { return nil }

type schedulerTransport func(*http.Request) (*http.Response, error)

func (f schedulerTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSchedulerSubscriptionRefresh_RetryUsesFreshContextAndExecution(t *testing.T) {
	now := time.Date(2026, 9, 9, 1, 2, 3, 0, time.UTC)
	var output bytes.Buffer
	redactor := logging.NewRedactor("scheduler-secret")
	reporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, new(slog.LevelVar), "daemon", redactor)), redactor)
	var backendIDs []string
	downloader := subscription.NewDownloader(subscription.DownloaderOptions{Client: &http.Client{Transport: schedulerTransport(func(r *http.Request) (*http.Response, error) {
		metadata, _ := logging.OperationFromContext(r.Context())
		backendIDs = append(backendIDs, metadata.ID)
		if len(backendIDs) == 1 {
			return nil, &os.PathError{Op: "read", Path: "/private/scheduler-secret", Err: os.ErrPermission}
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("proxies: []\nrules: [MATCH,DIRECT]\n")), Request: r}, nil
	})}})
	root := t.TempDir()
	service, err := subscription.Open(subscription.ServiceOptions{CatalogPath: filepath.Join(root, "catalog.yaml"), CacheDir: filepath.Join(root, "cache"), Downloader: downloader, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := service.Add("scheduled", "https://example.invalid/subscribe?token=scheduler-secret", subscription.ProxyModeDirect)
	if err != nil {
		t.Fatal(err)
	}
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	manager := runtimeapi.New(runtimeapi.Options{Subscriptions: service, Settings: settings, RuntimeConfig: filepath.Join(root, "runtime.yaml"), Controller: schedulerController{}, StagingDir: filepath.Join(root, "staging"), ValidateConfig: func(context.Context, string) error { return nil }, DiagnosticReporter: reporter})
	var entranceIDs []string
	var returned []error
	refresh := schedulerSubscriptionRefresh(func(ctx context.Context, op runtimeapi.Operation, id string) (subscription.PublicProfile, error) {
		metadata, _ := logging.OperationFromContext(ctx)
		entranceIDs = append(entranceIDs, metadata.ID)
		if op.Source != "scheduler" || id != profile.ID || metadata.Name != "subscription.refresh" {
			t.Errorf("scheduler metadata/name/source mismatch")
		}
		result, err := manager.RefreshSubscription(ctx, op, id)
		returned = append(returned, err)
		return result, err
	}, func() time.Time { return now })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var waits []time.Duration
	scheduler := subscription.NewScheduler(subscription.SchedulerOptions{Snapshot: service.Snapshot, Refresh: refresh, Now: func() time.Time { return now }, Jitter: func(string, time.Duration) time.Duration { return 0 }, After: func(wait time.Duration) <-chan time.Time {
		waits = append(waits, wait)
		if len(backendIDs) >= 2 {
			cancel()
			return nil
		}
		now = now.Add(wait)
		tick := make(chan time.Time, 1)
		tick <- now
		return tick
	}})
	if err := scheduler.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	if len(backendIDs) != 2 || len(entranceIDs) != 2 || len(returned) != 2 {
		t.Fatalf("executions=%d entries=%d results=%d", len(backendIDs), len(entranceIDs), len(returned))
	}
	wantIDs := []string{"scheduler-" + profile.ID + "-20260909T010203.000000000", "scheduler-" + profile.ID + "-20260909T010303.000000000"}
	for i, want := range wantIDs {
		if entranceIDs[i] != want || backendIDs[i] != want {
			t.Errorf("attempt %d entrance=%q backend=%q want=%q", i, entranceIDs[i], backendIDs[i], want)
		}
	}
	if !diagnostics.AlreadyReported(returned[0]) || returned[1] != nil || !errors.Is(returned[0], os.ErrPermission) {
		t.Fatalf("failure marker/cause or success lost: first=%v second=%v", returned[0], returned[1])
	}
	if len(waits) != 2 || waits[0] != time.Minute {
		t.Fatalf("waits=%v", waits)
	}
	records := decodeSchedulerDiagnostics(t, output.String())
	if len(records) != 2 || records[0]["msg"] != "operation.failed" || records[1]["msg"] != "operation.succeeded" {
		t.Fatalf("records=%v", records)
	}
	for i, record := range records {
		if record["operation_id"] != wantIDs[i] || record["operation"] != "subscription.refresh" || record["component"] != "runtime" {
			t.Fatalf("record=%v", record)
		}
	}
	if !strings.Contains(records[0]["cause"].(string), "path operation read: permission denied") || strings.Contains(output.String(), "scheduler-secret") || strings.Contains(output.String(), "example.invalid") {
		t.Fatal("unsafe or missing failure cause")
	}
	if !service.Snapshot().Public().Profiles[0].Cached {
		t.Fatal("retry did not commit cache")
	}
}

func decodeSchedulerDiagnostics(t *testing.T, output string) []map[string]any {
	t.Helper()
	var result []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(line))
		if _, err := decoder.Token(); err != nil {
			t.Fatal(err)
		}
		record := map[string]any{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				t.Fatal(err)
			}
			name := key.(string)
			if _, exists := record[name]; exists {
				t.Fatalf("duplicate JSON key %q", name)
			}
			var value any
			if err := decoder.Decode(&value); err != nil {
				t.Fatal(err)
			}
			record[name] = value
		}
		if _, err := decoder.Token(); err != nil {
			t.Fatal(err)
		}
		result = append(result, record)
	}
	return result
}

func TestSchedulerGeoIPRefresh_ContextPerAttempt(t *testing.T) {
	now := time.Date(2026, 9, 9, 1, 2, 3, 0, time.UTC)
	var ids []string
	failure := diagnostics.MarkReported(os.ErrPermission)
	refresh := schedulerGeoIPRefresh(func(ctx context.Context, op runtimeapi.Operation) (geoip.Status, error) {
		metadata, _ := logging.OperationFromContext(ctx)
		if metadata.ID != op.ID || metadata.Name != "geoip.update" || op.Source != "scheduler" {
			t.Errorf("missing GeoIP operation context: %#v", metadata)
		}
		ids = append(ids, op.ID)
		return geoip.Status{}, failure
	}, func() time.Time { return now })
	for range 2 {
		if err := refresh(context.Background()); err != failure {
			t.Fatal("refresh error identity lost")
		}
		now = now.Add(time.Second)
	}
	if ids[0] != "scheduler-geoip-20260909T010203.000000000" || ids[1] != "scheduler-geoip-20260909T010204.000000000" {
		t.Fatalf("ids=%v", ids)
	}
}

func TestRunSchedulers_ReportsLoopFailureAndJoinsBoth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstCleaned := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseSecond := make(chan struct{})
	secondCleaned := make(chan struct{})
	var output bytes.Buffer
	redactor := logging.NewRedactor()
	reporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, new(slog.LevelVar), "daemon", redactor)), redactor)
	reported := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- runSchedulers(ctx, func(context.Context) error { defer close(firstCleaned); return os.ErrPermission }, func(ctx context.Context) error {
			close(secondStarted)
			<-ctx.Done()
			<-releaseSecond
			close(secondCleaned)
			return ctx.Err()
		}, func(ctx context.Context, record diagnostics.Record) { reporter(ctx, record); reported <- struct{}{} }, func(string, error) { t.Error("legacy called with reporter") })
	}()
	// Cleanup always releases both loops even when a missing diagnostic fails the test.
	t.Cleanup(func() {
		cancel()
		close(releaseSecond)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run=%v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("schedulers did not join")
		}
		select {
		case <-secondCleaned:
		default:
			t.Error("second cleanup missing")
		}
	})
	<-secondStarted
	<-firstCleaned
	select {
	case <-reported:
	case <-time.After(time.Second):
		t.Fatal("loop failure not reported")
	}
	select {
	case <-done:
		t.Fatal("runner returned before parent cancellation")
	default:
	}
	cancel()
	select {
	case <-done:
		t.Fatal("runner returned before second cleanup")
	default:
	}
	records := decodeSchedulerDiagnostics(t, output.String())
	if len(records) != 1 || records[0]["component"] != "subscription-scheduler" || records[0]["msg"] != "background.failed" || records[0]["cause"] != "permission denied" {
		t.Fatalf("records=%v", records)
	}
}

func TestRunSchedulers_FailureClassificationAndLegacy(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, test := range []struct {
			name        string
			err         error
			cancelFirst bool
			want        int
		}{
			{"deadline", context.DeadlineExceeded, false, 1},
			{"upstream cancel", context.Canceled, false, 1},
			{"cancel", context.Canceled, true, 0},
			{"mixed cancel", errors.Join(context.Canceled, os.ErrPermission), true, 1},
			{"mixed deadline", errors.Join(context.DeadlineExceeded, os.ErrPermission), true, 1},
			{"marked", diagnostics.MarkReported(os.ErrPermission), true, 0},
			{"marked sibling", errors.Join(diagnostics.MarkReported(context.Canceled), os.ErrPermission), true, 1},
		} {
			t.Run(fmt.Sprintf("%s/legacy=%v", test.name, legacy), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				calls := 0
				check := func(component string, err error) {
					calls++
					if component != "geoip-scheduler" || err != test.err {
						t.Error("loop result changed")
					}
					cancel()
				}
				var reporter diagnostics.Reporter
				callback := check
				if !legacy {
					reporter = func(_ context.Context, record diagnostics.Record) {
						if record.Level != slog.LevelError || record.Event != "background.failed" {
							t.Error("failure event/level changed")
						}
						check(record.Component, record.Err)
					}
					callback = func(string, error) { t.Error("both diagnostic outlets called") }
				}
				err := runSchedulers(ctx, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func(context.Context) error {
					if test.cancelFirst {
						cancel()
					}
					return test.err
				}, reporter, callback)
				if err != nil || calls != test.want {
					t.Fatalf("Run=%v calls=%d want=%d", err, calls, test.want)
				}
			})
		}
	}
}

type schedulerGeoIPService struct{ runtimeapi.GeoIPService }

func (schedulerGeoIPService) Status() geoip.Status {
	return geoip.Status{Country: geoip.DatabaseStatus{Available: true}, ASN: geoip.DatabaseStatus{Available: true}}
}

type schedulerGeoIPCandidate struct{ committed, cleaned bool }

func (*schedulerGeoIPCandidate) Identity() string { return "prepared-pair" }
func (*schedulerGeoIPCandidate) Valid() bool      { return true }
func (c *schedulerGeoIPCandidate) Commit() error  { c.committed = true; return nil }
func (c *schedulerGeoIPCandidate) Cleanup()       { c.cleaned = true }

func TestSchedulerGeoIPRefresh_FailureStillChecksNextRound(t *testing.T) {
	now := time.Date(2026, 9, 9, 1, 2, 3, 0, time.UTC)
	var output bytes.Buffer
	redactor := logging.NewRedactor()
	reporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, new(slog.LevelVar), "daemon", redactor)), redactor)
	var ids []string
	candidate := &schedulerGeoIPCandidate{}
	manager := runtimeapi.New(runtimeapi.Options{GeoIP: schedulerGeoIPService{}, DiagnosticReporter: reporter, PrepareGeoIP: func(ctx context.Context) (runtimeapi.GeoIPCandidate, error) {
		meta, _ := logging.OperationFromContext(ctx)
		ids = append(ids, meta.ID)
		if len(ids) == 1 {
			return nil, os.ErrPermission
		}
		return candidate, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checks := 0
	waits := 0
	scheduler := geoip.Scheduler{NeedsUpdate: func(time.Time, time.Duration) bool { checks++; return true }, Refresh: schedulerGeoIPRefresh(manager.UpdateGeoIP, func() time.Time { return now }), Now: func() time.Time { return now }, After: func(wait time.Duration) <-chan time.Time {
		waits++
		if wait != geoip.DefaultCheckInterval {
			t.Errorf("wait=%v", wait)
		}
		if waits == 2 {
			cancel()
			return nil
		}
		now = now.Add(wait)
		tick := make(chan time.Time, 1)
		tick <- now
		return tick
	}}
	if err := scheduler.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if checks != 2 || len(ids) != 2 || ids[0] != "scheduler-geoip-20260909T010203.000000000" || ids[1] != "scheduler-geoip-20260910T010203.000000000" || !candidate.committed || !candidate.cleaned {
		t.Fatalf("checks=%d ids=%v candidate=%+v", checks, ids, candidate)
	}
	records := decodeSchedulerDiagnostics(t, output.String())
	if len(records) != 1 || records[0]["msg"] != "operation.failed" || records[0]["operation_id"] != ids[0] || records[0]["operation"] != "geoip.update" || records[0]["cause"] != "api error (internal): permission denied" {
		t.Fatalf("records=%v", records)
	}
}

func TestSchedulerSubscriptionRefresh_FallbackKeepsAttemptID(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy.Close()
	var proxyConnectStarts, proxyConnectDones atomic.Int32
	trace := &httptrace.ClientTrace{
		ConnectStart: func(network, address string) {
			proxyConnectStarts.Add(1)
			if network != "tcp" || address != proxyURL.Host {
				t.Errorf("proxy connect start network=%q address=%q want tcp %q", network, address, proxyURL.Host)
			}
		},
		ConnectDone: func(network, address string, err error) {
			proxyConnectDones.Add(1)
			if network != "tcp" || address != proxyURL.Host {
				t.Errorf("proxy connect done network=%q address=%q want tcp %q", network, address, proxyURL.Host)
			}
			if err == nil {
				t.Error("proxy connect unexpectedly succeeded")
			}
		},
	}
	var directID string
	downloader := subscription.NewDownloader(subscription.DownloaderOptions{ProxyURL: proxyURL, Client: &http.Client{Transport: schedulerTransport(func(request *http.Request) (*http.Response, error) {
		meta, _ := logging.OperationFromContext(request.Context())
		directID = meta.ID
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("proxies: []\n")), Request: request}, nil
	})}})
	root := t.TempDir()
	service, err := subscription.Open(subscription.ServiceOptions{CatalogPath: filepath.Join(root, "catalog.yaml"), CacheDir: filepath.Join(root, "cache"), Downloader: downloader})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := service.Add("fallback", "http://example.invalid/subscribe?token=fallback-secret", subscription.ProxyModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	var output bytes.Buffer
	redactor := logging.NewRedactor("fallback-secret")
	manager := runtimeapi.New(runtimeapi.Options{Subscriptions: service, Settings: settings, StagingDir: filepath.Join(root, "staging"), RuntimeConfig: filepath.Join(root, "runtime.yaml"), Controller: schedulerController{}, ValidateConfig: func(context.Context, string) error { return nil }, DiagnosticReporter: logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, new(slog.LevelVar), "daemon", redactor)), redactor)})
	var entranceID string
	clocks := 0
	refresh := schedulerSubscriptionRefresh(func(ctx context.Context, op runtimeapi.Operation, id string) (subscription.PublicProfile, error) {
		meta, _ := logging.OperationFromContext(ctx)
		entranceID = meta.ID
		return manager.RefreshSubscription(ctx, op, id)
	}, func() time.Time { clocks++; return time.Date(2026, 9, 9, 1, 2, 3, 0, time.UTC) })
	ctx := httptrace.WithClientTrace(context.Background(), trace)
	if err := refresh(ctx, profile.ID); err != nil {
		t.Fatal(err)
	}
	want := "scheduler-" + profile.ID + "-20260909T010203.000000000"
	if proxyConnectStarts.Load() != 1 || proxyConnectDones.Load() != 1 || clocks != 1 || entranceID != want || directID != want || !service.Snapshot().Public().Profiles[0].Cached {
		t.Fatalf("proxy connect starts=%d dones=%d clocks=%d entrance=%q direct=%q", proxyConnectStarts.Load(), proxyConnectDones.Load(), clocks, entranceID, directID)
	}
	records := decodeSchedulerDiagnostics(t, output.String())
	if len(records) != 1 || records[0]["msg"] != "operation.succeeded" || records[0]["operation_id"] != want {
		t.Fatalf("records=%v", records)
	}
	if strings.Contains(output.String(), "fallback-secret") || strings.Contains(output.String(), "example.invalid") {
		t.Fatal("fallback secret leaked")
	}
}

func TestRunSchedulers_ShutdownJoinsBothFailuresAndCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 2)
	var subscriptionCleaned, geoIPCleaned atomic.Bool
	var output bytes.Buffer
	redactor := logging.NewRedactor()
	reporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, new(slog.LevelVar), "daemon", redactor)), redactor)
	done := make(chan error, 1)
	go func() {
		done <- runSchedulers(ctx, func(ctx context.Context) error {
			defer subscriptionCleaned.Store(true)
			started <- struct{}{}
			<-ctx.Done()
			return errors.Join(ctx.Err(), os.ErrPermission)
		}, func(ctx context.Context) error {
			defer geoIPCleaned.Store(true)
			started <- struct{}{}
			<-ctx.Done()
			return errors.Join(ctx.Err(), io.ErrUnexpectedEOF)
		}, func(ctx context.Context, record diagnostics.Record) {
			if record.Component == "subscription-scheduler" && !subscriptionCleaned.Load() || record.Component == "geoip-scheduler" && !geoIPCleaned.Load() {
				t.Error("loop reported before cleanup")
			}
			reporter(ctx, record)
		}, nil)
	}()
	<-started
	<-started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("both schedulers were not joined")
	}
	if !subscriptionCleaned.Load() || !geoIPCleaned.Load() {
		t.Fatal("scheduler returned before both cleanups")
	}
	records := decodeSchedulerDiagnostics(t, output.String())
	if len(records) != 2 {
		t.Fatalf("records=%v", records)
	}
	components := map[string]bool{}
	for _, record := range records {
		component := record["component"].(string)
		if components[component] || record["level"] != "ERROR" || record["msg"] != "background.failed" {
			t.Fatalf("record=%v", record)
		}
		components[component] = true
		if _, ok := record["operation_id"]; ok {
			t.Fatal("loop failure invented operation ID")
		}
	}
	if !components["subscription-scheduler"] || !components["geoip-scheduler"] {
		t.Fatalf("components=%v", components)
	}
}
