package runtime

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

func TestOperationDiagnostics_SameIDConcurrencyAndReplayReportsOnce(t *testing.T) {
	saveEntered := make(chan struct{})
	releaseSave := make(chan struct{})
	var saves atomic.Int64
	var reports atomic.Int64
	manager := newTestManager(Options{
		Settings: config.Defaults(), SettingsPath: "settings.yaml", Logging: &recordingLoggingRuntime{dir: "logs"},
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves.Add(1)
			close(saveEntered)
			<-releaseSave
			return config.CommitResult{Committed: true, Warning: errors.New("sync warning")}, nil
		},
		DiagnosticReporter: func(context.Context, diagnostics.Record) { reports.Add(1) },
	})

	op := Operation{ID: "shared-warning", Source: "test"}
	first := make(chan error, 1)
	go func() {
		_, err := manager.UpdateLogging(context.Background(), op, LoggingUpdate{Level: stringPointer("debug")})
		first <- err
	}()
	select {
	case <-saveEntered:
	case <-time.After(time.Second):
		t.Fatal("first execution did not begin saving")
	}
	second := make(chan error, 1)
	go func() {
		_, err := manager.UpdateLogging(context.Background(), op, LoggingUpdate{Level: stringPointer("debug")})
		second <- err
	}()
	close(releaseSave)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateLogging(context.Background(), op, LoggingUpdate{Level: stringPointer("debug")}); err != nil {
		t.Fatal(err)
	}
	if saves.Load() != 1 || reports.Load() != 1 {
		t.Fatalf("saves=%d reports=%d", saves.Load(), reports.Load())
	}
}

func TestOperationDiagnostics_DirectAndSaturatedExecutionReportWarnings(t *testing.T) {
	var mu sync.Mutex
	var records []diagnostics.Record
	var emptyIDReports atomic.Int64
	manager := newTestManager(Options{
		DiagnosticReporter: func(ctx context.Context, record diagnostics.Record) {
			operation, ok := logging.OperationFromContext(ctx)
			if !ok {
				emptyIDReports.Add(1)
			} else if operation.ID != "saturated" {
				t.Errorf("unexpected operation metadata: %#v", operation)
			}
			mu.Lock()
			records = append(records, record)
			mu.Unlock()
		},
	})

	if _, err := manager.doOperation(context.Background(), "logging:", func(ctx context.Context) (any, error) {
		collectWarning(ctx, "settings", "persist.warning", errors.New("empty ID warning"))
		return struct{}{}, nil
	}); err != nil {
		t.Fatal(err)
	}

	manager.operationsMu.Lock()
	for index := 0; index < 256; index++ {
		manager.operations["pending-"+strconv.Itoa(index)] = &operationEntry{done: make(chan struct{})}
	}
	manager.operationsMu.Unlock()
	if _, err := manager.doOperation(context.Background(), "logging:saturated", func(ctx context.Context) (any, error) {
		collectWarning(ctx, "settings", "persist.warning", errors.New("saturated warning"))
		return struct{}{}, nil
	}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if emptyIDReports.Load() != 1 || len(records) != 2 || records[1].Err.Error() != "saturated warning" {
		t.Fatalf("records=%#v", records)
	}
}

func TestOperationDiagnostics_SeparateIDsKeepWarningsSeparate(t *testing.T) {
	firstWarning := errors.New("first warning")
	secondWarning := errors.New("second warning")
	var mu sync.Mutex
	byID := make(map[string]error)
	manager := newTestManager(Options{
		DiagnosticReporter: func(ctx context.Context, record diagnostics.Record) {
			operation, ok := logging.OperationFromContext(ctx)
			if !ok {
				t.Errorf("record missing operation metadata: %#v", record)
				return
			}
			mu.Lock()
			byID[operation.ID] = record.Err
			mu.Unlock()
		},
	})

	for _, test := range []struct {
		key string
		err error
	}{
		{key: "logging:first", err: firstWarning},
		{key: "logging:second", err: secondWarning},
	} {
		test := test
		if _, err := manager.doOperation(context.Background(), test.key, func(ctx context.Context) (any, error) {
			collectWarning(ctx, "settings", "persist.warning", test.err)
			return struct{}{}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !errors.Is(byID["first"], firstWarning) || !errors.Is(byID["second"], secondWarning) || len(byID) != 2 {
		t.Fatalf("warnings=%#v", byID)
	}
}
