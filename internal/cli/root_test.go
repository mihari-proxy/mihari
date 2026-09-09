package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/update"
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

func TestPrepareLocalRootSkippedForSelfChannel(t *testing.T) {
	previousElevationCheck := elevate.Check
	t.Cleanup(func() { elevate.Check = previousElevationCheck })
	elevate.Check = func() bool { return true }

	t.Setenv("MIHARI_DATA", t.TempDir())
	called := 0
	code := Execute(context.Background(), []string{"self", "channel"}, io.Discard, io.Discard, Dependencies{
		PrepareLocalRoot: func() error {
			called++
			return errors.New("prepare should not run")
		},
	})
	if code != ExitOK || called != 0 {
		t.Fatalf("code=%d called=%d", code, called)
	}
}

func TestPrepareLocalRootRunsForSelfUpdate(t *testing.T) {
	previousElevationCheck := elevate.Check
	t.Cleanup(func() { elevate.Check = previousElevationCheck })
	elevate.Check = func() bool { return true }

	called := 0
	code := Execute(context.Background(), []string{"self", "update"}, io.Discard, io.Discard, Dependencies{
		SelfUpdater: &fakeSelfUpdater{result: update.Result{Version: "v1.0.0", Channel: "main"}},
		PrepareLocalRoot: func() error {
			called++
			return nil
		},
	})
	if code != ExitOK || called != 1 {
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

func TestPrepareLocalRootFailurePreservesAPIError(t *testing.T) {
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
	if got, want := stderr.String(), "{\"schema\":\"mihari.error/v1\",\"error\":{\"code\":\"data_failure\",\"message\":\"resolve Mihari data root\"}}\n"; got != want {
		t.Fatalf("stderr=%q want=%q", got, want)
	}
}

func TestExecuteJSON_DataFailureKeepsSingleSafeEnvelope(t *testing.T) {
	const secret = "control-token-not-for-cli-output"
	const subscriptionURL = "https://user:token@secret.example/sub?token=control-token-not-for-cli-output"
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Execute(context.Background(), []string{"status", "--json"}, stdout, stderr, Dependencies{
		SetupError: diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "persist settings"}, errors.New(subscriptionURL+"\nproxies:\n  - name: secret")),
	})
	if code != ExitData || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got, want := stderr.String(), "{\"schema\":\"mihari.error/v1\",\"error\":{\"code\":\"data_failure\",\"message\":\"persist settings\"}}\n"; got != want {
		t.Fatalf("stderr=%q want=%q", got, want)
	}
	if strings.Count(stderr.String(), "\n") != 1 || strings.Contains(stderr.String(), secret) || strings.Contains(stderr.String(), subscriptionURL) || strings.Contains(stderr.String(), "proxies:") {
		t.Fatalf("CLI JSON output leaked diagnostics or wrote multiple envelopes: %q", stderr.String())
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
