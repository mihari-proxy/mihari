package daemon

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestRun_StartupCleanupAfterOwnershipFailureRemainsInHistory(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	owned, cleaned := false, false
	err = Run(ctx, Options{
		Listen: func(context.Context) (net.Listener, error) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			owned = err == nil
			return listener, err
		},
		StartupCleanup: func(context.Context) error {
			if !owned {
				t.Error("cleanup ran before listener ownership")
			}
			cleaned = true
			return errors.New("remove fixture.exe.old-123: access denied")
		},
		OnReady: func() error {
			if !cleaned {
				t.Error("ready before startup cleanup")
			}
			page := history.List("", 0, 100)
			if len(page.Records) != 1 {
				t.Errorf("startup failure history=%+v", page)
			} else {
				detail := history.Get(page.Records[0].ID).Diagnostic
				if detail == nil || detail.Severity != "warning" || !strings.Contains(detail.Detail, "fixture.exe.old-123") {
					t.Errorf("lost cleanup detail: %+v", detail)
				}
			}
			cancel()
			return nil
		},
		DiagnosticReporter: diagnostics.NewOwner(history, nil).Report,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRun_StartupCleanupSkipsFailedOwnershipAndValidation(t *testing.T) {
	for _, validation := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		_ = Run(ctx, Options{
			ValidationMode: validation,
			Listen: func(context.Context) (net.Listener, error) {
				if !validation {
					return nil, errors.New("another owner")
				}
				return net.Listen("tcp", "127.0.0.1:0")
			},
			StartupCleanup: func(context.Context) error { calls++; return nil },
			OnReady:        func() error { cancel(); return nil },
		})
		cancel()
		if calls != 0 {
			t.Fatalf("cleanup calls=%d validation=%v", calls, validation)
		}
	}
}
