package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
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
			cmd := newServiceApplyCommand(Dependencies{ServiceApply: func(ctx context.Context, req app.InstallRequest) (app.InstallResult, error) {
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

func (s entryUpdateSpy) Update(context.Context, string, string, string) (update.Result, error) {
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
		deps.ServiceApply = func(context.Context, app.InstallRequest) (app.InstallResult, error) { return app.InstallResult{}, nil }
		Execute(context.Background(), []string{"service", command, "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, deps)
		if len(events) != 0 {
			t.Fatalf("service %s initialized data before transaction: %v", command, events)
		}
	}
}
