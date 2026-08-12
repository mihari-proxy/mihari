package main

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestTUIRelaunchArgsStartsDefaultTUI(t *testing.T) {
	binary := `C:\Program Files\Mihari\mihari.exe`
	if got, want := tuiRelaunchArgs(binary), []string{binary}; !slices.Equal(got, want) {
		t.Fatalf("args=%q want=%q", got, want)
	}
}

func TestInteractiveTerminal_RejectsRedirectedStreams(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	output, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	if isInteractiveTerminal(input, output) {
		t.Fatal("regular files must not be treated as an interactive terminal")
	}
}

func TestRestartServiceAfterReplaceIgnoresOnlyNotInstalled(t *testing.T) {
	notInstalled := protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihari service is not installed"}
	if err := restartServiceAfterReplace(func() error { return notInstalled }); err != nil {
		t.Fatalf("not-installed error=%v", err)
	}

	restartFailed := protocol.APIError{Code: protocol.CodeInvalidState, Message: "service failed to start in time"}
	err := restartServiceAfterReplace(func() error { return restartFailed })
	if !errors.As(err, &restartFailed) {
		t.Fatalf("restart failure was suppressed: %v", err)
	}
}
