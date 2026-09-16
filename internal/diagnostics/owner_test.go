package diagnostics

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestOwner_ExpectedFailureHasHistoryEvenAtDebug(t *testing.T) {
	history, err := NewHistory(HistoryOptions{InstanceID: "expected"})
	if err != nil {
		t.Fatal(err)
	}
	owner := NewOwner(history, nil)
	failure := ReportError(context.Background(), owner.Report, Record{Level: slog.LevelDebug, Err: protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid fixture argument"}})
	snapshot, ok := Snapshot(failure)
	if !ok || snapshot.ID == "" || len(history.List("", 0, 100).Records) != 1 {
		t.Fatalf("file severity filtered an actual failure: %+v", snapshot)
	}
}

func TestOwner_HistoryDoesNotDependOnFileOutlet(t *testing.T) {
	for _, file := range []Reporter{nil, func(context.Context, Record) {}} {
		history := testHistory(t, 10, 4096)
		owner := NewOwner(history, file)
		owner.Report(context.Background(), Record{Component: "runtime", Event: "refresh.failed", Level: slog.LevelError, Err: errors.New("token=fixture-token")})
		page := history.List("", 0, 10)
		if len(page.Records) != 1 {
			t.Fatalf("without an active file logger: %+v", page)
		}
		got := history.Get(page.Records[0].ID)
		if got.Diagnostic.Detail != "token=fixture-token" || got.Diagnostic.Severity != "error" || got.Diagnostic.Component != "runtime" {
			t.Fatalf("captured occurrence = %+v", got.Diagnostic)
		}
	}
}

func TestOwner_SuccessAndDebugDoNotBecomeFailures(t *testing.T) {
	history := testHistory(t, 10, 4096)
	var fileRecords int
	owner := NewOwner(history, func(context.Context, Record) { fileRecords++ })
	owner.Report(context.Background(), Record{Event: "operation.succeeded", Level: slog.LevelInfo})
	owner.Report(context.Background(), Record{Event: "remote.response", Level: slog.LevelDebug, Err: protocol.APIError{Code: protocol.CodeDataFailure, Message: "remote summary"}})
	if len(history.List("", 0, 10).Records) != 0 || fileRecords != 2 {
		t.Fatal("debug/success events became failure history or disappeared from the file outlet")
	}
}

func TestOwner_BackgroundOccurrencesRemainIndependent(t *testing.T) {
	history := testHistory(t, 10, 4096)
	owner := NewOwner(history, nil)
	for range 2 {
		owner.Report(context.Background(), Record{Event: "poll.failed", Level: slog.LevelWarn, Err: errors.New("same upstream failure")})
	}
	page := history.List("", 0, 10)
	if len(page.Records) != 2 || page.Records[0].ID == page.Records[1].ID || page.Records[0].Severity != "warning" {
		t.Fatalf("background occurrences were merged or misclassified: %+v", page)
	}
}

func TestReportError_PreservesOccurrenceIDAndClassification(t *testing.T) {
	history := testHistory(t, 10, 8192)
	var fileCalls int
	owner := NewOwner(history, func(context.Context, Record) { fileCalls++ })
	cause := errors.New("password=fixture-original")
	err := Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "save settings"}, cause)
	ctx := WithOperation(context.Background(), OperationMetadata{ID: "operation-123", Name: "settings.update"})
	reported := ReportError(ctx, owner.Report, Record{Component: "runtime", Event: "settings.failed", Level: slog.LevelError, Err: err})
	var api protocol.APIError
	if !errors.As(reported, &api) || api.Code != protocol.CodeDataFailure || api.Message != "save settings" || api.Diagnostic == nil {
		t.Fatalf("reported classification = %+v", api)
	}
	if !errors.Is(reported, cause) || api.Diagnostic.OperationID != "operation-123" {
		t.Fatal("cause or operation identity lost")
	}
	stored := history.Get(api.Diagnostic.ID)
	if stored.Diagnostic == nil || *stored.Diagnostic != *api.Diagnostic || stored.Diagnostic.Summary != "save settings" {
		t.Fatalf("outcome differs from history: %+v", stored)
	}
	_ = ReportError(ctx, owner.Report, Record{Level: slog.LevelError, Err: reported})
	if fileCalls != 1 || len(history.List("", 0, 50).Records) != 1 {
		t.Fatal("forwarding an occurrence published it twice")
	}
	_ = ReportError(ctx, owner.Report, Record{Level: slog.LevelError, Err: err})
	if len(history.List("", 0, 50).Records) != 2 {
		t.Fatal("a separate execution of the original error was merged")
	}
}

func TestReportError_NoLoggerStillCapturesDetails(t *testing.T) {
	if ReportError(context.Background(), nil, Record{}) != nil {
		t.Fatal("nil error became a failure")
	}
	reported := ReportError(context.Background(), nil, Record{Err: errors.New("token=fixture-token"), Level: slog.LevelError})
	snapshot, ok := Snapshot(reported)
	if !ok || snapshot.Detail != "token=fixture-token" || snapshot.State != protocol.DiagnosticAvailable {
		t.Fatalf("local error has no original detail: %+v", snapshot)
	}
}

func TestOwner_NormalCancellationDoesNotEnterHistory(t *testing.T) {
	history := testHistory(t, 10, 4096)
	owner := NewOwner(history, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	owner.Report(ctx, Record{Level: slog.LevelInfo, Err: context.Canceled})
	if len(history.List("", 0, 10).Records) != 0 {
		t.Fatal("normal cancellation became an error-history occurrence")
	}
	owner.Report(ctx, Record{Level: slog.LevelError, Err: errors.Join(context.Canceled, errors.New("cleanup fixture failed"))})
	if len(history.List("", 0, 10).Records) != 1 {
		t.Fatal("cancellation hid an actual cleanup failure")
	}
}

func TestOwner_ElapsedDeadlineRemainsInspectable(t *testing.T) {
	history := testHistory(t, 10, 4096)
	owner := NewOwner(history, nil)
	ctx, cancel := context.WithDeadline(context.Background(), timeInPast())
	defer cancel()
	owner.Report(ctx, Record{Level: slog.LevelInfo, Err: ctx.Err()})
	if len(history.List("", 0, 10).Records) != 1 {
		t.Fatal("request timeout was hidden as active cancellation")
	}
}
