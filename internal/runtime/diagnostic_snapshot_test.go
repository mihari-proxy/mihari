package runtime

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/config"
	"net/http"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestOperationDiagnostic_SnapshotSurvivesReplayWithoutAnotherOccurrence(t *testing.T) {
	history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{InstanceID: "runtime"})
	if err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(Options{DiagnosticReporter: diagnostics.NewOwner(history, nil).Report})
	cause := errors.New("token=fixture-original\npermission denied")
	executions := 0
	run := func(key string) protocol.Diagnostic {
		t.Helper()
		_, failure := manager.doOperation(context.Background(), key, func(context.Context) (any, error) {
			executions++
			return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "save settings"}, cause)
		})
		var api protocol.APIError
		if !errors.Is(failure, cause) || !errors.As(failure, &api) || api.Code != protocol.CodeDataFailure {
			t.Fatalf("classification or cause lost: %v", failure)
		}
		snapshot, ok := diagnostics.Snapshot(failure)
		if !ok || snapshot.ID == "" || !strings.Contains(snapshot.Detail, cause.Error()) {
			t.Fatalf("returned failure has no original occurrence: %+v", snapshot)
		}
		return snapshot
	}
	first := run("logging:first")
	replay := run("logging:first")
	second := run("logging:second")
	if first.ID != replay.ID || first.ID == second.ID || executions != 2 {
		t.Fatalf("identity or replay mismatch: %s %s %s executions=%d", first.ID, replay.ID, second.ID, executions)
	}
	if records := history.List("", 0, 100).Records; len(records) != 2 || records[0].OperationID != "first" || records[1].OperationID != "second" {
		t.Fatalf("history does not describe actual executions: %+v", records)
	}
}

func TestOperationDiagnostic_CommittedWarningReturnsOnReplay(t *testing.T) {
	history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{InstanceID: "warnings"})
	if err != nil {
		t.Fatal(err)
	}
	saves := 0
	manager := newTestManager(Options{
		Settings: config.Defaults(), SettingsPath: "settings.yaml", Logging: &recordingLoggingRuntime{dir: "logs"},
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves++
			return config.CommitResult{Committed: true, Warning: errors.New("sync /private/token=fixture-original")}, nil
		},
		DiagnosticReporter: diagnostics.NewOwner(history, nil).Report,
	})
	var firstID string
	for attempt := 0; attempt < 2; attempt++ {
		ctx, outcome := diagnostics.WithResult(context.Background())
		status, err := manager.UpdateLogging(ctx, Operation{ID: "committed-warning"}, LoggingUpdate{Level: stringPointer("debug")})
		if err != nil || status.Revision != 1 || status.Level != "debug" {
			t.Fatalf("committed result changed: %+v %v", status, err)
		}
		warnings := outcome.Warnings()
		if len(warnings.Warnings) != 1 || warnings.Warnings[0].Diagnostic == nil {
			t.Fatalf("warning disappeared: %+v", warnings)
		}
		detail := warnings.Warnings[0].Diagnostic
		if detail.ID == "" || detail.OperationID != "committed-warning" {
			t.Fatalf("warning identity missing: %+v", detail)
		}
		stored := history.Get(detail.ID)
		if stored.Diagnostic == nil || !strings.Contains(stored.Diagnostic.Detail, "token=fixture-original") {
			t.Fatalf("original warning missing: %+v", stored)
		}
		if attempt == 0 {
			firstID = detail.ID
		} else if detail.ID != firstID {
			t.Fatal("replay changed warning identity")
		}
	}
	if saves != 1 || len(history.List("", 0, 100).Records) != 1 {
		t.Fatal("warning replay re-executed or republished")
	}
}

func TestOperationDiagnostic_MissingReporterStillReturnsOriginalDetail(t *testing.T) {
	manager := newTestManager(Options{})
	_, err := manager.doOperation(context.Background(), "logging:no-file", func(context.Context) (any, error) {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "save settings"}, errors.New("password=fixture-local"))
	})
	snapshot, ok := diagnostics.Snapshot(err)
	if !ok || snapshot.OperationID != "no-file" || !strings.Contains(snapshot.Detail, "password=fixture-local") {
		t.Fatalf("missing reporter discarded diagnostic: %+v", snapshot)
	}
}

func TestSubscriptionDiagnostic_FirstFetchFailureReturnsWarningWithoutReplay(t *testing.T) {
	cause := errors.New("first fetch token=fixture-first-download")
	fetcher := &scriptedSubscriptionFetcher{entries: []scriptedSubscriptionFetch{{err: cause}}}
	manager, _, _, _ := subscriptionManagerWithDownloader(t, http.NotFoundHandler(), fetcher)
	history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{InstanceID: "first-fetch"})
	if err != nil {
		t.Fatal(err)
	}
	manager.diagnosticReporter = diagnostics.NewOwner(history, nil).Report
	var id, recordID string
	for attempt := 0; attempt < 2; attempt++ {
		ctx, receipt := diagnostics.WithResult(context.Background())
		profile, err := manager.AddSubscription(ctx, Operation{ID: "add-partial", Source: "test"}, AddSubscriptionInput{Name: "fixture", URL: "https://fixture.invalid/sub"})
		if err != nil || profile.ID == "" || profile.Cached {
			t.Fatal("failed fetch invalidated registration")
		}
		if attempt == 0 {
			id = profile.ID
		} else if profile.ID != id {
			t.Fatal("replay registered another subscription")
		}
		warnings := receipt.Warnings()
		if len(warnings.Warnings) != 1 || warnings.Warnings[0].Diagnostic == nil {
			t.Fatal("successful add response lost first-fetch warning")
		}
		snapshot := warnings.Warnings[0].Diagnostic
		if snapshot.ID == "" || snapshot.OperationID != "add-partial-fetch" {
			t.Fatal("warning did not reuse child owner")
		}
		detail := history.Get(snapshot.ID)
		if detail.Diagnostic == nil || !strings.Contains(detail.Diagnostic.Detail, cause.Error()) {
			t.Fatal("original first-fetch cause lost")
		}
		if attempt == 0 {
			recordID = snapshot.ID
		} else if snapshot.ID != recordID {
			t.Fatal("replay published new child occurrence")
		}
	}
	if fetcher.CallCount() != 1 || len(history.List("", 0, 100).Records) != 1 {
		t.Fatal("first-fetch failure executed or published more than once")
	}
}
