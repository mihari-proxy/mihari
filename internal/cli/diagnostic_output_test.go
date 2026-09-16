package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/update"
)

func TestExecute_OriginalSetupDetails(t *testing.T) {
	const original = "open /private/password=fixture-original.yaml\nhttps://u:p@fixture.invalid/sub?token=abc"
	for _, prepare := range []bool{false, true} {
		for _, asJSON := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{false: "setup", true: "prepare"}[prepare], map[bool]string{false: "text", true: "json"}[asJSON]}, "/"), func(t *testing.T) {
				deps := Dependencies{SetupError: errors.New(original)}
				if prepare {
					deps.SetupError = nil
					deps.PrepareLocalRoot = func() error { return errors.New(original) }
				}
				args := []string{"status"}
				if asJSON {
					args = append(args, "--json")
				}
				var stderr bytes.Buffer
				code := Execute(context.Background(), args, io.Discard, &stderr, deps)
				if code != ExitData {
					t.Fatalf("exit changed: %d", code)
				}
				if asJSON {
					var envelope protocol.ErrorEnvelope
					decoder := json.NewDecoder(&stderr)
					if err := decoder.Decode(&envelope); err != nil {
						t.Fatal(err)
					}
					if envelope.Error.Code != protocol.CodeDataFailure || envelope.Error.Diagnostic == nil || !strings.Contains(envelope.Error.Diagnostic.Detail, original) {
						t.Fatalf("JSON lost raw cause: %+v", envelope.Error)
					}
					if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
						t.Fatalf("multiple output envelopes: %v", err)
					}
				} else if !strings.Contains(stderr.String(), "Code: data_failure") || !strings.Contains(stderr.String(), "Details:\n") || !strings.Contains(stderr.String(), original) {
					t.Fatalf("text lost cause or classification: %s", stderr.String())
				}
			})
		}
	}
}

func TestExecute_TerminalEscapesControlsWhileJSONPreservesOriginal(t *testing.T) {
	const raw = "token=fixture\x1b]52;c;ZXhhbXBsZQ==\a\r\x00\u009b\nnext line"
	failure := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "save\x1b[31m settings"}, errors.New(raw))
	var text bytes.Buffer
	if code := Execute(context.Background(), []string{"status"}, io.Discard, &text, Dependencies{SetupError: failure}); code != ExitData {
		t.Fatal(code)
	}
	if strings.ContainsAny(text.String(), "\x1b\a\r\x00\u009b") || !strings.Contains(text.String(), `\x1b]52`) || !strings.Contains(text.String(), "\nnext line") {
		t.Fatalf("unsafe or missing terminal detail: %q", text.String())
	}
	var output bytes.Buffer
	Execute(context.Background(), []string{"status", "--json"}, io.Discard, &output, Dependencies{SetupError: failure})
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Diagnostic == nil || !strings.Contains(envelope.Error.Diagnostic.Detail, raw) {
		t.Fatal("display escaping modified the captured JSON text")
	}
}

type warningRuntimeClient struct{ RuntimeClient }

func (warningRuntimeClient) RestartCore(context.Context, protocol.MutationRequest) (protocol.MutationResult, error) {
	return protocol.MutationResult{Schema: "mihari/v1", OperationID: "warning-operation", WarningOutcome: protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: "cleanup warning", Diagnostic: &protocol.Diagnostic{State: protocol.DiagnosticAvailable, Detail: "token=fixture-warning", Summary: "cleanup warning"}}}}}, nil
}

func TestExecute_CommittedWarningKeepsSuccessfulExitAndOriginalDetail(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		args := []string{"core", "restart"}
		if asJSON {
			args = append(args, "--json")
		}
		code := Execute(context.Background(), args, &stdout, &stderr, Dependencies{RuntimeClient: warningRuntimeClient{}, NewOperationID: func() string { return "warning-operation" }})
		if code != ExitOK {
			t.Fatalf("warning changed success to %d", code)
		}
		if asJSON {
			var result protocol.MutationResult
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if stderr.Len() != 0 || len(result.Warnings) != 1 || result.Warnings[0].Diagnostic.Detail != "token=fixture-warning" {
				t.Fatal("JSON warning lost or mixed with text")
			}
		} else if !strings.Contains(stdout.String(), "completed") || !strings.Contains(stderr.String(), "Warning: cleanup warning") || !strings.Contains(stderr.String(), "token=fixture-warning") {
			t.Fatalf("text warning missing: stdout=%s stderr=%s", stdout.String(), stderr.String())
		}
	}
}

func TestExecute_PurgePreservesOriginalFailure(t *testing.T) {
	previous := elevate.Check
	t.Cleanup(func() { elevate.Check = previous })
	elevate.Check = func() bool { return true }
	for _, stage := range []string{"close", "run"} {
		for _, asJSON := range []bool{false, true} {
			cause := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "fixture failure summary"}, errors.New("token=fixture-purge-original"))
			uninstaller := &fakePurgeUninstaller{}
			deps := Dependencies{Uninstaller: uninstaller}
			if stage == "close" {
				deps.CloseForPurgeUninstall = func() error { return cause }
			} else {
				uninstaller.err = cause
			}
			args := []string{"service", "uninstall", "--purge", "--yes"}
			if asJSON {
				args = append(args, "--json")
			}
			var stderr bytes.Buffer
			if code := Execute(context.Background(), args, io.Discard, &stderr, deps); code != ExitInvalidState {
				t.Fatal("purge exit changed")
			}
			if !strings.Contains(stderr.String(), "token=fixture-purge-original") {
				t.Fatal("purge cause discarded")
			}
			if (stage == "close" && uninstaller.calls != 0) || (stage == "run" && uninstaller.calls != 1) {
				t.Fatal("purge replayed or ran after failed cleanup")
			}
			if asJSON {
				var result protocol.ErrorEnvelope
				if json.Unmarshal(stderr.Bytes(), &result) != nil || result.Error.Diagnostic == nil {
					t.Fatal("JSON diagnostic missing")
				}
			}
		}
	}
}

type firstDiagnosticWriteFailure struct {
	bytes.Buffer
	calls int
}

func (w *firstDiagnosticWriteFailure) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		return 0, errors.New("token=fixture-progress-write")
	}
	return w.Buffer.Write(p)
}
func TestExecute_PurgeProgressWriteFailureIsNotLost(t *testing.T) {
	previous := elevate.Check
	t.Cleanup(func() { elevate.Check = previous })
	elevate.Check = func() bool { return true }
	uninstaller := &fakePurgeUninstaller{}
	stderr := &firstDiagnosticWriteFailure{}
	code := Execute(context.Background(), []string{"service", "uninstall", "--purge", "--yes"}, io.Discard, stderr, Dependencies{Uninstaller: uninstaller})
	if code == ExitOK || uninstaller.calls != 1 || !strings.Contains(stderr.String(), "token=fixture-progress-write") {
		t.Fatal("progress write failure was discarded or uninstall repeated")
	}
}

func TestExecute_SelfReplacementWarningDoesNotDiscardFailureCause(t *testing.T) {
	t.Setenv("MIHARI_DATA", t.TempDir())
	previous := elevate.Check
	t.Cleanup(func() { elevate.Check = previous })
	elevate.Check = func() bool { return true }
	preview, err := update.NewReplacementPreview(update.ReplacementCandidate{Version: "v2.0.0", SHA256: strings.Repeat("a", 64), Channel: "main"}, update.ReplacementSnapshot{Targets: []update.ReplacementTarget{{Roles: []string{"binary"}, Path: "/fixture/binary", Exists: true, Version: "v3.0.0"}}})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeSelfUpdater{prepared: update.PreparedUpdate{Available: true, Version: "v2.0.0", Channel: "main", Preview: preview}, applyErr: diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "self apply failed"}, errors.New("token=fixture-self-apply"))}
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"self", "update", "--yes", "--json"}, &stdout, &stderr, Dependencies{SelfUpdater: fake})
	var result protocol.ErrorEnvelope
	if code != ExitData || json.Unmarshal(stderr.Bytes(), &result) != nil || result.Error.Diagnostic == nil || !strings.Contains(result.Error.Diagnostic.Detail, "token=fixture-self-apply") || stdout.Len() != 0 || fake.calls != 1 {
		t.Fatal("self update warning discarded actual cause or replayed apply")
	}
}

func TestExecute_SelfReplacementSuccessKeepsWarningInJSON(t *testing.T) {
	t.Setenv("MIHARI_DATA", t.TempDir())
	previous := elevate.Check
	t.Cleanup(func() { elevate.Check = previous })
	elevate.Check = func() bool { return true }
	preview, err := update.NewReplacementPreview(update.ReplacementCandidate{Version: "v2.0.0", SHA256: strings.Repeat("a", 64), Channel: "main"}, update.ReplacementSnapshot{Targets: []update.ReplacementTarget{{Roles: []string{"binary"}, Path: "/fixture/binary", Exists: true, Version: "v3.0.0"}}})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeSelfUpdater{prepared: update.PreparedUpdate{Available: true, Version: "v2.0.0", Channel: "main", Preview: preview}, result: update.Result{Updated: true, Version: "v2.0.0", Channel: "main"}}
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"self", "update", "--yes", "--json"}, &stdout, &stderr, Dependencies{SelfUpdater: fake})
	var result struct {
		Updated bool `json:"updated"`
		protocol.WarningOutcome
	}
	if code != ExitOK || json.Unmarshal(stdout.Bytes(), &result) != nil || stderr.Len() != 0 || !result.Updated || len(result.Warnings) != 1 || result.Warnings[0].Diagnostic == nil || result.Warnings[0].Diagnostic.Detail != update.ReplacementWarning(preview) || fake.calls != 1 {
		t.Fatal("successful JSON mixed text, lost warning, or replayed apply")
	}
}
