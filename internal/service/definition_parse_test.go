package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestParseLaunchdPrint_MissingSystemService(t *testing.T) {
	for _, stderr := range []string{
		"Could not find service \"system/mihari\".\n",
		"Could not find service \"mihari\" in domain for system\n",
		"Bad request.\nCould not find service \"mihari\" in domain for system\n",
	} {
		t.Run(stderr, func(t *testing.T) {
			loaded, running, pid, err := parseLaunchdPrint(CommandResult{ExitCode: 113, Stderr: []byte(stderr)}, "system/mihari")
			if err != nil || loaded || running || pid != 0 {
				t.Fatalf("loaded=%v running=%v pid=%d err=%v", loaded, running, pid, err)
			}
		})
	}
}

func TestParseLaunchdPrint_UnexpectedFailurePreservesOutput(t *testing.T) {
	for _, tc := range []struct{ stdout, stderr string }{
		{"", "Could not find service \"other\" in domain for system\n"},
		{"", "Could not find service \"mihari\" in domain for gui/501\n"},
		{"", "Could not find domain for system\n"},
		{"", "Permission denied: token=fixture-original\n"},
		{"", "Bad request.\nCould not find service \"mihari\" in domain for system\nPermission denied\n"},
		{"unexpected stdout", "Could not find service \"mihari\" in domain for system\n"},
	} {
		t.Run(tc.stdout+tc.stderr, func(t *testing.T) {
			loaded, running, pid, err := parseLaunchdPrint(CommandResult{ExitCode: 113, Stdout: []byte(tc.stdout), Stderr: []byte(tc.stderr)}, "system/mihari")
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState || loaded || running || pid != 0 {
				t.Fatalf("loaded=%v running=%v pid=%d err=%v", loaded, running, pid, err)
			}
			detail := diagnostics.Capture(err).Text
			for _, want := range []string{"113", tc.stdout, tc.stderr} {
				if !strings.Contains(detail, want) {
					t.Fatalf("missing %q in %s", want, detail)
				}
			}
		})
	}
}
