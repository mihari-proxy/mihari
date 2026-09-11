package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type statusHintError struct{ cause error }

func (e statusHintError) Error() string { return e.cause.Error() + "; " + e.Hint() }
func (e statusHintError) Unwrap() error { return e.cause }
func (e statusHintError) Hint() string {
	return "if the control credential was changed, restart the service"
}

func TestStatus_PreservesCategoriesAndAuthenticationHint(t *testing.T) {
	for _, tc := range []struct {
		code protocol.ErrorCode
		exit int
	}{{protocol.CodeDaemonUnavailable, 3}, {protocol.CodePermissionDenied, 5}, {protocol.CodeDataFailure, 9}, {protocol.CodeInvalidArgument, 2}, {protocol.CodeInvalidState, 4}} {
		for _, isJSON := range []bool{false, true} {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			args := []string{"status"}
			if isJSON {
				args = append(args, "--json")
			}
			api := protocol.APIError{Code: tc.code, Message: "operation failed", Details: map[string]any{"scope": "control"}}
			var err error = api
			if tc.code == protocol.CodePermissionDenied {
				err = statusHintError{api}
			}
			if exit := Execute(context.Background(), args, stdout, stderr, Dependencies{StatusClient: fakeStatusClient{err: err}}); exit != tc.exit {
				t.Fatalf("code %s exit=%d", tc.code, exit)
			}
			if isJSON {
				var envelope protocol.ErrorEnvelope
				if e := json.Unmarshal(stderr.Bytes(), &envelope); e != nil {
					t.Fatal(e)
				}
				if envelope.Error.Code != tc.code || envelope.Error.Message != api.Message || envelope.Error.Details["scope"] != "control" || strings.Contains(stderr.String(), "restart") {
					t.Fatal("JSON error changed")
				}
			} else if tc.code == protocol.CodePermissionDenied && !strings.Contains(stderr.String(), "restart the service") {
				t.Fatal("text authentication hint was discarded")
			}
		}
	}
}

type fakeStatusClient struct {
	status protocol.Status
	err    error
}

func (f fakeStatusClient) Status(context.Context) (protocol.Status, error) {
	return f.status, f.err
}

func TestStatusJSON(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := Execute(context.Background(), []string{"status", "--json"}, stdout, stderr, Dependencies{
		StatusClient: fakeStatusClient{status: protocol.Status{
			Schema:          "mihari/v1",
			ProtocolVersion: "v1",
			DaemonVersion:   "dev",
			Revision:        2,
			Health:          "ok",
			StartedAt:       time.Unix(100, 0).UTC(),
		}},
	})
	if code != ExitOK || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	want := `{"schema":"mihari/v1","protocol_version":"v1","daemon_version":"dev","revision":2,"health":"ok","started_at":"1970-01-01T00:01:40Z"}` + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout=%q want=%q", stdout.String(), want)
	}
}

func TestStatusHumanOutput(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := Execute(context.Background(), []string{"status"}, stdout, stderr, Dependencies{
		StatusClient: fakeStatusClient{status: protocol.Status{
			DaemonVersion: "v0.1.0",
			Revision:      4,
			Health:        "ok",
			StartedAt:     time.Unix(100, 0).UTC(),
		}},
	})
	if code != ExitOK || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	want := "Daemon: v0.1.0\nHealth: ok\nRevision: 4\nStarted: 1970-01-01T00:01:40Z\n"
	if stdout.String() != want {
		t.Fatalf("stdout=%q want=%q", stdout.String(), want)
	}
}

func TestStatusHumanOutputIncludesLastError(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := Execute(context.Background(), []string{"status"}, stdout, stderr, Dependencies{
		StatusClient: fakeStatusClient{status: protocol.Status{
			DaemonVersion: "v0.1.0",
			Revision:      4,
			Health:        "degraded",
			LastError:     "managed port is unavailable",
			StartedAt:     time.Unix(100, 0).UTC(),
		}},
	})
	if code != ExitOK || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	want := "Daemon: v0.1.0\nHealth: degraded\nError: managed port is unavailable\nRevision: 4\nStarted: 1970-01-01T00:01:40Z\n"
	if stdout.String() != want {
		t.Fatalf("stdout=%q want=%q", stdout.String(), want)
	}
}

func TestDaemonUnavailableExitCodeAndJSONError(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := Execute(context.Background(), []string{"status", "--json"}, stdout, stderr, Dependencies{
		StatusClient: fakeStatusClient{err: errors.New("dial failed")},
	})
	if code != ExitDaemonUnavailable || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("stderr=%q err=%v", stderr.String(), err)
	}
	if envelope.Error.Code != protocol.CodeDaemonUnavailable {
		t.Fatalf("error=%#v", envelope.Error)
	}
}

func TestSetupErrorUsesDataExitCode(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := Execute(context.Background(), []string{"status", "--json"}, stdout, stderr, Dependencies{
		SetupError: errors.New("credential unavailable"),
	})
	if code != ExitData {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestUnknownCommandUsesUsageExitCode(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := Execute(context.Background(), []string{"does-not-exist", "--json"}, stdout, stderr, Dependencies{})
	if code != ExitUsage {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("stderr=%q err=%v", stderr.String(), err)
	}
	if envelope.Error.Code != protocol.CodeInvalidArgument {
		t.Fatalf("error=%#v", envelope.Error)
	}
}

func TestDaemonCommandRunsInjectedDaemon(t *testing.T) {
	called := false
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := Execute(context.Background(), []string{"daemon"}, stdout, stderr, Dependencies{
		RunDaemon: func(context.Context) error {
			called = true
			return nil
		},
	})
	if code != ExitOK || !called {
		t.Fatalf("code=%d called=%v stderr=%q", code, called, stderr.String())
	}
}
