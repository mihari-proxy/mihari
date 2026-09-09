package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/update"
)

func TestServiceApply_StrictRequestAndProcessAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		euid       int
		valid      bool
	}{
		{"recover", `{"schema":"mihari.install-request/v1","operation":"recover"}`, 0, true},
		{"duplicate", `{"schema":"mihari.install-request/v1","operation":"recover","operation":"recover"}`, 0, false},
		{"unknown", `{"schema":"mihari.install-request/v1","operation":"recover","token":"secret"}`, 0, false},
		{"nonroot", `{"schema":"mihari.install-request/v1","operation":"recover"}`, 1000, false},
		{"root-claim", `{"schema":"mihari.install-request/v1","operation":"recover","euid":0}`, 1000, false},
		{"oversize", strings.Repeat(" ", app.MaxInstallRequestBytes) + `{}`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := filepath.Join(t.TempDir(), "request.json")
			if err := os.WriteFile(request, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			cmd := newServiceApplyCommand(Dependencies{ServiceApply: func(ctx context.Context, req app.InstallRequest, consent update.ReplacementConsent) (app.InstallResult, error) {
				calls++
				if req.Operation != "recover" {
					t.Fatal("wrong decoded request")
				}
				return app.InstallResult{Schema: app.InstallResultSchema, Changed: true, ServiceStatus: "stopped", TransactionID: strings.Repeat("a", 32)}, nil
			}}, &runOptions{json: true}, func() int { return tc.euid })
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"--request", request})
			err := cmd.ExecuteContext(context.Background())
			if tc.valid {
				if err != nil || calls != 1 {
					t.Fatalf("request was not dispatched: calls=%d err=%v", calls, err)
				}
				if _, err := app.DecodeInstallResult(&out); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || calls != 0 {
				t.Fatalf("invalid request reached apply: calls=%d err=%v", calls, err)
			}
		})
	}
}

type entryUpdateSpy struct{ events *[]string }

func (s entryUpdateSpy) Prepare(context.Context, string, string, string) (update.PreparedUpdate, error) {
	return update.PreparedUpdate{}, nil
}
func (s entryUpdateSpy) ApplyPrepared(context.Context, update.PreparedUpdate) (update.Result, error) {
	*s.events = append(*s.events, "binary-only-update")
	return update.Result{Updated: true, Channel: "main"}, nil
}
func TestInstallEntry_FullCLIAvoidsBasePreparation(t *testing.T) {
	t.Setenv("MIHARI_DATA", t.TempDir())
	elevate.SetChecker(func() bool { return true })
	defer elevate.SetChecker(nil)
	events := []string{}
	deps := Dependencies{PrepareLocalRoot: func() error { events = append(events, "open-base"); return nil }, SelfUpdateChannel: func(context.Context) (string, error) { events = append(events, "inspect-service"); return "main", nil }, SelfUpdater: entryUpdateSpy{&events}}
	code := Execute(context.Background(), []string{"self", "update", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, deps)
	if code != 0 || strings.Join(events, ",") != "inspect-service,binary-only-update" {
		t.Fatalf("CLI touched B before service inspection: %v code=%d", events, code)
	}
	for _, command := range []string{"apply", "install", "reinstall", "start", "stop", "uninstall", "status"} {
		events = nil
		deps.ServiceApply = func(context.Context, app.InstallRequest, update.ReplacementConsent) (app.InstallResult, error) {
			return app.InstallResult{}, nil
		}
		Execute(context.Background(), []string{"service", command, "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, deps)
		if len(events) != 0 {
			t.Fatalf("service %s initialized data before transaction: %v", command, events)
		}
	}
}

func TestServiceApply_ConfirmationFlags(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		valid    bool
		yes      bool
		expected string
	}{
		{name: "recover", valid: true},
		{name: "explicit", args: []string{"--yes"}, valid: true, yes: true},
		{name: "bound", args: []string{"--yes", "--expected-preview", strings.Repeat("a", 64)}, valid: true, yes: true, expected: strings.Repeat("a", 64)},
		{name: "missing-yes", args: []string{"--expected-preview", strings.Repeat("a", 64)}},
		{name: "empty", args: []string{"--yes", "--expected-preview="}},
		{name: "uppercase", args: []string{"--yes", "--expected-preview", strings.Repeat("A", 64)}},
		{name: "short", args: []string{"--yes", "--expected-preview", "ab"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MIHARI_DATA", t.TempDir())
			request := filepath.Join(t.TempDir(), "request.json")
			if err := os.WriteFile(request, []byte(`{"schema":"mihari.install-request/v1","operation":"recover"}`), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			cmd := newServiceApplyCommand(Dependencies{ServiceApply: func(_ context.Context, req app.InstallRequest, consent update.ReplacementConsent) (app.InstallResult, error) {
				calls++
				if req.Operation != "recover" || consent.Yes != tc.yes || consent.ExpectedPreview != tc.expected {
					t.Fatalf("unexpected dispatch req=%+v consent=%+v", req, consent)
				}
				return app.InstallResult{Schema: app.InstallResultSchema, ServiceStatus: "stopped", TransactionID: strings.Repeat("a", 32)}, nil
			}}, &runOptions{json: true}, func() int { return 0 })
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(append([]string{"--request", request}, tc.args...))
			err := cmd.ExecuteContext(context.Background())
			if tc.valid {
				if err != nil || calls != 1 {
					t.Fatalf("calls=%d err=%v", calls, err)
				}
				if _, err := app.DecodeInstallResult(&out); err != nil {
					t.Fatal(err)
				}
			} else {
				var api protocol.APIError
				if calls != 0 || !errors.As(err, &api) || api.Code != protocol.CodeInvalidArgument {
					t.Fatalf("calls=%d err=%v", calls, err)
				}
			}
		})
	}
}

func TestServiceApply_JSONWarningFailure(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("full service apply command requires root")
	}
	t.Setenv("MIHARI_DATA", t.TempDir())
	previous := elevate.Check
	t.Cleanup(func() { elevate.Check = previous })
	elevate.Check = func() bool { return true }
	request := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(request, []byte(`{"schema":"mihari.install-request/v1","operation":"recover"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"service", "apply", "--request", request, "--yes", "--json"}, &out, &stderr, Dependencies{ServiceApply: func(_ context.Context, _ app.InstallRequest, c update.ReplacementConsent) (app.InstallResult, error) {
		if !c.Yes || c.Warn == nil {
			t.Fatal("missing warning callback or consent")
		}
		if err := c.Warn("Safe compatibility warning."); err != nil {
			return app.InstallResult{}, err
		}
		return app.InstallResult{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "installation changed; start again"}
	}})
	if code != ExitInvalidState || out.Len() != 0 || !json.Valid(stderr.Bytes()) || !strings.Contains(stderr.String(), "Safe compatibility warning.") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), stderr.String())
	}
}
