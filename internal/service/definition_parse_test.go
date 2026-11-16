package service

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestSystemdLogicalLines_ContinuationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want []string
	}{
		{"single-backslash", "Key=first\\\nsecond\n", []string{"Key=first second"}},
		{"double-backslash", "Key=first\\\\\nNext=second\n", []string{`Key=first\\`, "Next=second"}},
		{"triple-backslash", "Key=first\\\\\\\nsecond\n", []string{`Key=first\\ second`}},
		{"four-backslashes", "Key=first\\\\\\\\\nNext=second\n", []string{`Key=first\\\\`, "Next=second"}},
		{"backslash-space", "Key=first\\ \nNext=second\n", []string{`Key=first\`, "Next=second"}},
		{"backslash-tab", "Key=first\\\t\nNext=second\n", []string{`Key=first\`, "Next=second"}},
		{"preserve-indentation", "Key=first\\\n \tsecond\n", []string{"Key=first  \tsecond"}},
		{"multiple-continuations", "Key=first\\\nsecond\\\nthird\n", []string{"Key=first second third"}},
		{"comment-block", "Key=first\\\n # comment \\\n\t; comment \\\nsecond\n", []string{"Key=first second"}},
		{"comment-before-exec", "# comment \\\nExecStartPre=/usr/bin/false\n", []string{"ExecStartPre=/usr/bin/false"}},
		{"blank-line-ends-continuation", "Key=first\\\n\nNext=second\n", []string{"Key=first", "Next=second"}},
		{"crlf", "Key=first\\\r\nsecond\r\n", []string{"Key=first second"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := systemdLogicalLines([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("logical lines=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseSystemdUnit_ContinuationSeparatesArguments(t *testing.T) {
	for _, tc := range []struct {
		name    string
		comment string
	}{
		{"adjacent-lines", ""},
		{"comment-block", "# comment\n; comment\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "[Service]\nExecStart=/usr/local/lib/mihari/mihari\\\n" + tc.comment + "daemon\\\n--system-service\n"
			got, err := parseSystemdUnit([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			if got.Binary != "/usr/local/lib/mihari/mihari" || !slices.Equal(got.Args, []string{"daemon", "--system-service"}) {
				t.Fatalf("continued ExecStart parsed as %+v", got)
			}
		})
	}
}

func TestParsePrintDisabled_CurrentAndLegacyStates(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		disabled  bool
	}{
		{"legacy disabled", "{\n\"mihari\" => true\n}\n", true},
		{"legacy enabled", "{\n\"mihari\" => false\n}\n", false},
		{"current disabled", "disabled services = {\n\"mihari\" => disabled\n}\n", true},
		{"current enabled", "disabled services = {\n\"mihari\" => enabled\n}\n", false},
		{"default enabled", "disabled services = {\n\"other\" => disabled\n}\n", false},
		{"empty overrides", "disabled services = {\n}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePrintDisabled([]byte(tc.raw), "mihari")
			if err != nil || got != tc.disabled {
				t.Fatalf("disabled=%v err=%v", got, err)
			}
		})
	}
}

func TestParsePrintDisabled_RejectsAmbiguousOrMalformedOutput(t *testing.T) {
	for _, raw := range []string{
		"", "permission denied", "disabled services = {\n", "disabled services = {\n}\ntrailing",
		"disabled services = {\n\"mihari\" => unknown\n}\n",
		"disabled services = {\n\"mihari\" => enabled\n\"mihari\" => disabled\n}\n",
		"gui/501 disabled services = {\n\"mihari\" => enabled\n}\n",
	} {
		if _, err := parsePrintDisabled([]byte(raw), "mihari"); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestParsePrintDisabled_UnknownOutputPreservesDiagnostic(t *testing.T) {
	raw := []byte("system => {\n\t\"mihari\" => unexpected\n}\n")
	_, err := parsePrintDisabled(raw, "mihari")
	if err == nil || !strings.Contains(diagnostics.Capture(err).Text, string(raw)) {
		t.Fatalf("original output missing: %v", err)
	}
}

func TestParseLaunchdPrint_UnknownSuccessOutputPreservesDiagnostic(t *testing.T) {
	raw := []byte("system/mihari = {\n unexpected fixture\n}\n")
	_, _, _, err := parseLaunchdPrint(CommandResult{Stdout: raw}, "system/mihari")
	if err == nil || !strings.Contains(diagnostics.Capture(err).Text, string(raw)) {
		t.Fatalf("original output missing: %v", err)
	}
}

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
