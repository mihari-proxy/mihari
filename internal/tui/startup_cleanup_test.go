package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestStartupCleanup_F2HistoryWithoutFileLogger(t *testing.T) {
	history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	runStartupCleanup(t.Context(), func(context.Context) error { return errors.New("remove fixture.exe.old-123: denied") }, diagnostics.NewOwner(history, nil).Report)
	page := history.List("", 0, 100)
	if len(page.Records) != 1 {
		t.Fatalf("history=%+v", page)
	}
	detail := history.Get(page.Records[0].ID).Diagnostic
	if detail == nil || detail.Severity != "warning" || !strings.Contains(detail.Detail, "fixture.exe.old-123") {
		t.Fatalf("diagnostic=%+v", detail)
	}
}

func TestRun_StartupCleanupAfterLoggingBeforeSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	loggingOpened, cleaned := false, false
	err := Run(ctx, Options{
		Input: strings.NewReader(""), Output: io.Discard, ErrorOutput: io.Discard,
		OpenLogging: func(context.Context) (LoggingResources, error) { loggingOpened = true; return LoggingResources{}, nil },
		StartupCleanup: func(context.Context) error {
			if !loggingOpened {
				t.Error("cleanup before logging initialized")
			}
			cleaned = true
			return errors.New("fixture cleanup failed")
		},
		Installation: InstallationActions{Inspect: func(context.Context) (protocol.InstallationStatus, error) {
			if !cleaned {
				t.Error("session started before cleanup")
			}
			cancel()
			return protocol.InstallationStatus{}, nil
		}},
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !cleaned {
		t.Fatal("startup cleanup never ran")
	}
}
