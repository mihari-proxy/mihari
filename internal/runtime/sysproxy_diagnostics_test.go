package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
)

func TestSystemProxyDiagnostic_BackendFailuresKeepCauseAndReplayJSON(t *testing.T) {
	for _, stage := range []string{"get", "enable", "disable", "readback", "save", "compensation"} {
		t.Run(stage, func(t *testing.T) {
			cause := &os.PathError{Op: "open", Path: "/private/sysproxy-secret", Err: os.ErrPermission}
			restoreCause := &os.PathError{Op: "rename", Path: "/private/sysproxy-secret", Err: os.ErrExist}
			backend := &hookSysProxyBackend{state: sysproxy.State{Enabled: true, Server: "127.0.0.1:9190"}}
			saves := 0
			switch stage {
			case "get":
				backend.getErr = cause
			case "enable", "compensation":
				backend.enableErr = cause

			case "disable":
				backend.disableErr = cause
			case "readback":
				backend.onGet = func() {
					if backend.getCalls == 2 {
						backend.getErr = cause
					}
				}
			}
			var output bytes.Buffer
			manager := newTestManager(Options{SysProxy: backend, Settings: defaultSysProxySettings(stage == "disable"), SettingsPath: "settings.yaml", SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
				saves++
				if stage == "save" {
					return config.CommitResult{}, cause
				}
				if stage == "compensation" && saves == 2 {
					return config.CommitResult{}, restoreCause
				}
				return config.CommitResult{Committed: true}, nil
			}, DiagnosticReporter: businessJSONReporter(&output)})
			op := Operation{ID: "sysproxy-" + stage, Source: "test"}
			invoke := func() error {
				if stage == "disable" {
					_, err := manager.DisableSystemProxy(context.Background(), op)
					return err
				}
				_, err := manager.EnableSystemProxy(context.Background(), op, false)
				return err
			}
			err := invoke()
			if !errors.Is(err, cause) {
				t.Fatalf("original backend cause lost: %v", err)
			}
			code := protocol.CodeUpstreamFailure
			if stage == "save" || stage == "compensation" || stage == "enable" {
				code = protocol.CodeDataFailure
			}
			if stage == "compensation" && !errors.Is(err, restoreCause) {
				t.Fatal("settings restore cause lost")
			}
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != code || !diagnostics.AlreadyReported(err) || strings.Contains(err.Error(), "sysproxy-secret") {
				t.Fatalf("unsafe/classification/marker error: %v", err)
			}
			if replay := invoke(); !errors.Is(replay, cause) || replay.Error() != err.Error() {
				t.Fatal("replay changed execution result")
			}
			name := "system_proxy.enable"
			if stage == "disable" {
				name = "system_proxy.disable"
			}
			assertBusinessFailureJSON(t, output.String(), op.ID, name, string(code))
		})
	}
}

func TestSystemProxyDiagnostic_NewCompensationFailureOutlivesReportedChild(t *testing.T) {
	childCause := errors.New("old child")
	child := diagnostics.MarkReported(diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "old child failed"}, childCause))
	newCause := &os.PathError{Op: "rename", Path: "/private/sysproxy-secret", Err: os.ErrPermission}
	var output bytes.Buffer
	manager := newTestManager(Options{Settings: defaultSysProxySettings(false), SettingsPath: "settings.yaml", SaveSettings: func(string, config.Settings) (config.CommitResult, error) { return config.CommitResult{}, newCause }, DiagnosticReporter: businessJSONReporter(&output)})
	_, err := manager.doOperation(context.Background(), "sysproxy-enable:new-aggregate", func(ctx context.Context) (any, error) {
		if lockErr := manager.lockMutation(ctx); lockErr != nil {
			return nil, lockErr
		}
		defer manager.unlock()
		return nil, manager.compensateSystemProxy(ctx, Operation{ID: "new-aggregate"}, settingsCandidate{before: defaultSysProxySettings(true)}, sysproxy.State{}, child, false)
	})
	if !errors.Is(err, childCause) || !errors.Is(err, newCause) || err.Error() != "apply mutation: mutation compensation failed" {
		t.Fatalf("aggregate lost causes or public contract: %v", err)
	}
	assertBusinessFailureJSON(t, output.String(), "new-aggregate", "system_proxy.enable", "data_failure")
}

func businessJSONReporter(output *bytes.Buffer) diagnostics.Reporter {
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	redactor := logging.NewRedactor("sysproxy-secret", "business-secret")
	return logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(output, level, "daemon", redactor)), redactor)
}

func assertBusinessFailureJSON(t *testing.T, output, id, name, code string) {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal([]byte(output), &record); err != nil {
		t.Fatalf("expected exactly one diagnostic: %v output=%s", err, output)
	}
	if record["operation_id"] != id || record["operation"] != name || record["level"] != "ERROR" || !strings.Contains(output, "api error ("+code+")") || record["msg"] != "operation.failed" {
		t.Fatalf("record=%#v", record)
	}
	if strings.Contains(output, "sysproxy-secret") || strings.Contains(output, "business-secret") || !strings.Contains(output, "permission denied") {
		t.Fatalf("unsafe or missing cause: %s", output)
	}
}

func TestSystemProxyDiagnostic_ForeignConflictIsDebug(t *testing.T) {
	for _, enable := range []bool{true, false} {
		var output bytes.Buffer
		backend := &sysproxy.FakeBackend{State: sysproxy.State{Enabled: true, Server: "192.0.2.1:8080"}}
		manager := newTestManager(Options{SysProxy: backend, Settings: defaultSysProxySettings(false), DiagnosticReporter: businessJSONReporter(&output)})
		op := Operation{ID: "foreign-conflict", Source: "test"}
		var err error
		if enable {
			_, err = manager.EnableSystemProxy(context.Background(), op, false)
		} else {
			_, err = manager.DisableSystemProxy(context.Background(), op)
		}
		var api protocol.APIError
		if !errors.As(err, &api) || backend.EnableCalls != 0 || backend.DisableCalls != 0 || manager.Snapshot().Revision != 0 {
			t.Fatalf("conflict changed state: %v", err)
		}
		if !strings.Contains(output.String(), `"level":"DEBUG"`) || strings.Contains(output.String(), `"level":"ERROR"`) {
			t.Fatalf("conflict logs=%s", output.String())
		}
	}
}
