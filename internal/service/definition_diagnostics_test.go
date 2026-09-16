package service

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"strings"
	"testing"
)

type diagnosticCommandRunner struct{ err error }

func (r diagnosticCommandRunner) Run(context.Context, []string) (CommandResult, error) {
	return CommandResult{}, r.err
}
func TestServiceCommand_PreservesOriginalCause(t *testing.T) {
	original := errors.New("systemctl: token=fixture-original")
	for _, err := range []error{original, errors.Join(protocol.APIError{Code: protocol.CodePermissionDenied, Message: "service denied", Details: map[string]any{"path": "fixture"}}, original)} {
		_, got := runAbsolute(context.Background(), diagnosticCommandRunner{err}, []string{"/fixture/systemctl"})
		if !errors.Is(got, original) {
			t.Fatalf("cause lost: %v", got)
		}
		var api protocol.APIError
		if !errors.As(got, &api) {
			t.Fatalf("classification lost: %v", got)
		}
		if api.Code == protocol.CodePermissionDenied && api.Details != nil || !strings.Contains(diagnostics.Capture(got).Text, "fixture") {
			t.Fatal("classification changed or original API details lost from diagnostic")
		}
	}
}

func TestServiceCommand_NonzeroExitPreservesOutput(t *testing.T) {
	err := requireZeroExit(CommandResult{ExitCode: 7, Stdout: []byte("stdout fixture"), Stderr: []byte("stderr token=fixture-original")})
	detail := diagnostics.Capture(err).Text
	for _, want := range []string{"7", "stdout fixture", "stderr token=fixture-original"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("missing %q: %s", want, detail)
		}
	}
}
