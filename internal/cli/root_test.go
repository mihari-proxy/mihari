package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestPrepareLocalRootSkippedForHelpCommand(t *testing.T) {
	called := 0
	code := Execute(context.Background(), []string{"help"}, io.Discard, io.Discard, Dependencies{
		PrepareLocalRoot: func() error {
			called++
			return errors.New("prepare should not run")
		},
	})
	if code != ExitOK || called != 0 {
		t.Fatalf("code=%d called=%d", code, called)
	}
}

func TestPrepareLocalRootSkippedForHelpFlag(t *testing.T) {
	called := 0
	code := Execute(context.Background(), []string{"--help"}, io.Discard, io.Discard, Dependencies{
		PrepareLocalRoot: func() error {
			called++
			return errors.New("prepare should not run")
		},
	})
	if code != ExitOK || called != 0 {
		t.Fatalf("code=%d called=%d", code, called)
	}
}

func TestPrepareLocalRootSkippedForDaemonHelp(t *testing.T) {
	called := 0
	code := Execute(context.Background(), []string{"daemon", "--help"}, io.Discard, io.Discard, Dependencies{
		PrepareLocalRoot: func() error {
			called++
			return errors.New("prepare should not run")
		},
	})
	if code != ExitOK || called != 0 {
		t.Fatalf("code=%d called=%d", code, called)
	}
}

func TestPrepareLocalRootSkippedForSelfVersion(t *testing.T) {
	called := 0
	code := Execute(context.Background(), []string{"self", "version"}, io.Discard, io.Discard, Dependencies{
		PrepareLocalRoot: func() error {
			called++
			return errors.New("prepare should not run")
		},
	})
	if code != ExitOK || called != 0 {
		t.Fatalf("code=%d called=%d", code, called)
	}
}

func TestPrepareLocalRootRunsForStatus(t *testing.T) {
	called := 0
	code := Execute(context.Background(), []string{"status"}, io.Discard, io.Discard, Dependencies{
		PrepareLocalRoot: func() error {
			called++
			return nil
		},
		StatusClient: fakeStatusClient{status: protocol.Status{
			DaemonVersion: "dev",
			Health:        "ok",
			StartedAt:     time.Unix(100, 0).UTC(),
		}},
	})
	if code != ExitOK || called != 1 {
		t.Fatalf("code=%d called=%d", code, called)
	}
}

func TestPrepareLocalRootFailureUsesDataExitCode(t *testing.T) {
	stderr := &bytes.Buffer{}
	code := Execute(context.Background(), []string{"status", "--json"}, io.Discard, stderr, Dependencies{
		PrepareLocalRoot: func() error {
			return protocol.APIError{Code: protocol.CodeDataFailure, Message: "resolve Mihari data root"}
		},
		SetupError: errors.New("stale copied setup error must not be used"),
	})
	if code != ExitData {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestHelpCommandStillHonorsSetupError(t *testing.T) {
	called := 0
	code := Execute(context.Background(), []string{"help"}, io.Discard, io.Discard, Dependencies{
		PrepareLocalRoot: func() error {
			called++
			return nil
		},
		SetupError: errors.New("credential unavailable"),
	})
	if code != ExitData || called != 0 {
		t.Fatalf("code=%d called=%d", code, called)
	}
}

func TestInteractiveRunTUIAfterPrepareLocalRoot(t *testing.T) {
	called := false
	code := Execute(context.Background(), []string{}, io.Discard, io.Discard, Dependencies{
		Interactive: true,
		PrepareLocalRoot: func() error {
			return nil
		},
		RunTUI: func(context.Context) error {
			called = true
			return nil
		},
	})
	if code != ExitOK || !called {
		t.Fatalf("code=%d called=%v", code, called)
	}
}
