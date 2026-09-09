package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

func configDiagnosticManager(t *testing.T) (*Manager, *reloadController, string) {
	t.Helper()
	var fetches atomic.Int32
	m, _, c, url := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		content := "proxies:\n  - {name: initial}\n"
		if fetches.Add(1) > 1 {
			content = "proxies:\n  - {name: candidate}\n"
		}
		_, _ = w.Write([]byte(content))
	}))
	p, err := m.AddSubscription(context.Background(), Operation{ID: "seed", Source: "test"}, AddSubscriptionInput{Name: "cached", URL: url})
	if err != nil || !p.Cached {
		t.Fatalf("seed: %v", err)
	}
	return m, c, p.ID
}

func configDiagnosticRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func configDiagnosticReadOnly(t *testing.T, dir string) {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("requires Unix directory permissions")
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0700); err != nil {
			t.Error(err)
		}
	})
}

func TestConfigDiagnostic_ReloadCompensation(t *testing.T) {
	for _, mode := range []string{"restored", "second_reload", "restore", "receipt", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			m, c, id := configDiagnosticManager(t)
			before := configDiagnosticRead(t, m.runtimeConfig)
			cacheBefore := configDiagnosticRead(t, m.subscriptions.CachePath(id))
			catalog := m.Subscriptions()
			snapshot := m.Snapshot()
			first := &os.PathError{Op: "open", Path: "/private/first-secret", Err: os.ErrPermission}
			second := &os.PathError{Op: "read", Path: "/private/second-secret", Err: os.ErrClosed}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			c.reload = func(got context.Context) error {
				calls++
				if calls == 1 {
					switch mode {
					case "restore":
						configDiagnosticReadOnly(t, filepath.Dir(m.runtimeConfig))
					case "receipt":
						configDiagnosticReadOnly(t, filepath.Dir(m.subscriptions.CachePath(id)))
					case "cancel":
						cancel()
					}
					return first
				}
				if mode == "cancel" {
					if got.Err() != context.Canceled {
						t.Error("ordinary rollback lost original canceled context")
					}
					return got.Err()
				}
				if mode == "second_reload" || mode == "restore" {
					return second
				}
				return nil
			}
			recorder := &coreDiagnosticRecorder{}
			var output bytes.Buffer
			level := new(slog.LevelVar)
			level.Set(slog.LevelDebug)
			redactor := logging.NewRedactor("first-secret", "second-secret")
			report := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, level, "daemon", redactor)), redactor)
			m.diagnosticReporter = func(ctx context.Context, record diagnostics.Record) {
				recorder.report(ctx, record)
				report(ctx, record)
			}
			_, err := m.RefreshSubscription(ctx, Operation{ID: "config-failure", Source: "test"}, id)
			wantMessage := "mihomo rejected generated configuration; previous configuration restored"
			wantCode := protocol.CodeUpstreamFailure
			degraded := mode != "restored"
			if degraded {
				wantMessage = "mihomo reload failed and rollback could not be confirmed"
			}
			if mode == "receipt" {
				wantCode = protocol.CodeDataFailure
				wantMessage = "subscription state rollback failed"
			}
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != wantCode || api.Message != wantMessage || (api.Details["degraded"] == true) != degraded {
				t.Fatalf("public contract changed: %v", err)
			}
			if !errors.Is(err, first) {
				t.Error("initial reload cause missing")
			}
			if (mode == "second_reload" || mode == "restore") && !errors.Is(err, second) {
				t.Error("second reload cause missing")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Error("rollback cancellation cause missing")
			}
			if mode == "restore" || mode == "receipt" {
				// This distinct filesystem failure must survive alongside both reload causes.
				if !hasConfigDiagnosticPath(err, "open", "create") {
					t.Error("restore filesystem cause missing")
				}
			}
			var record map[string]any
			if decodeErr := json.Unmarshal(output.Bytes(), &record); decodeErr != nil {
				t.Fatalf("expected one final JSON record: %v", decodeErr)
			}
			causeText, _ := record["cause"].(string)
			if record["operation_id"] != "config-failure" || record["operation"] != "subscription.refresh" || record["msg"] != "operation.failed" || record["level"] != "ERROR" || !strings.Contains(causeText, "path operation open") {
				t.Errorf("final JSON record=%#v", record)
			}
			if (mode == "second_reload" || mode == "restore") && !strings.Contains(causeText, "path operation read") {
				t.Error("second failure summary missing from JSON")
			}
			for _, secret := range []string{"first-secret", "second-secret", "proxies:"} {
				if strings.Contains(output.String(), secret) {
					t.Error("unsafe detail in diagnostic output")
				}
			}
			if calls != 2 {
				t.Fatalf("reload calls=%d", calls)
			}
			if mode != "restore" && !bytes.Equal(before, configDiagnosticRead(t, m.runtimeConfig)) {
				t.Error("previous config not restored")
			}
			if mode != "receipt" && !reflect.DeepEqual(catalog, m.Subscriptions()) {
				t.Error("catalog/cache generation not rolled back")
			}
			if mode == "restore" && bytes.Equal(before, configDiagnosticRead(t, m.runtimeConfig)) {
				t.Error("restore fault did not leave published candidate in place")
			}
			cacheAfter := configDiagnosticRead(t, m.subscriptions.CachePath(id))
			if (mode != "receipt") != bytes.Equal(cacheBefore, cacheAfter) {
				t.Error("cache rollback ordering changed")
			}

			after := m.Snapshot()
			if degraded {
				if after.Health != "degraded" || after.Config.LastError != "generated configuration rollback could not be confirmed" || after.Config.DesiredRevision <= after.Config.ObservedRevision || after.Revision != snapshot.Revision+1 {
					t.Fatalf("degraded state changed: %#v", after)
				}
			} else if !reflect.DeepEqual(snapshot, after) {
				t.Error("successful compensation changed state/revision")
			}
			records := recorder.snapshot()
			if len(records) != 1 || records[0].operation.ID != "config-failure" || records[0].record.Event != "operation.failed" || records[0].record.Level != slog.LevelError || !errors.Is(records[0].record.Err, first) || !diagnostics.AlreadyReported(err) {
				t.Errorf("final diagnostic missing or duplicated: %#v", records)
			}
		})
	}
}

// A tree walk distinguishes an actual restore IO error from the injected reload errors.
func hasConfigDiagnosticPath(err error, operations ...string) bool {
	if e, ok := err.(*os.PathError); ok {
		for _, op := range operations {
			if e.Op == op && !strings.HasPrefix(e.Path, "/private/") {
				return true
			}
		}
	}
	if e, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range e.Unwrap() {
			if hasConfigDiagnosticPath(child, operations...) {
				return true
			}
		}
	}
	if e, ok := err.(interface{ Unwrap() error }); ok {
		return hasConfigDiagnosticPath(e.Unwrap(), operations...)
	}
	return false
}

type configDiagnosticRunner struct{ err error }

func (r configDiagnosticRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return []byte("proxies:\n  password: candidate-secret"), r.err
}

func TestConfigDiagnostic_ValidationFailureSafeJSON(t *testing.T) {
	m, c, id := configDiagnosticManager(t)
	before := configDiagnosticRead(t, m.runtimeConfig)
	catalog := m.Subscriptions()
	snapshot := m.Snapshot()
	reloads := c.reloads
	cause := &exec.ExitError{Stderr: []byte("proxies:\n  password: candidate-secret")}
	m.validateConfig = func(ctx context.Context, path string) error {
		return core.ValidateConfig(ctx, configDiagnosticRunner{cause}, "synthetic-core", t.TempDir(), path)
	}
	var output bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	redactor := logging.NewRedactor("candidate-secret")
	m.diagnosticReporter = logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, level, "daemon", redactor)), redactor)
	_, err := m.RefreshSubscription(context.Background(), Operation{ID: "validation-failure", Source: "test"}, id)
	if !errors.Is(err, cause) || err.Error() != "mihomo configuration validation failed" {
		t.Fatalf("validation contract/cause: %v", err)
	}
	if !bytes.Equal(before, configDiagnosticRead(t, m.runtimeConfig)) || c.reloads != reloads || !reflect.DeepEqual(catalog, m.Subscriptions()) || !reflect.DeepEqual(snapshot, m.Snapshot()) {
		t.Fatal("validation changed last valid state or reloaded")
	}
	entries, readErr := os.ReadDir(m.stagingDir)
	if readErr != nil || len(entries) != 0 {
		t.Fatal("rejected candidate was not cleaned")
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("expected one JSON diagnostic: %v", err)
	}
	if record["operation_id"] != "validation-failure" || record["operation"] != "subscription.refresh" || record["msg"] != "operation.failed" || record["level"] != "ERROR" || !strings.Contains(record["cause"].(string), "command execution failed") {
		t.Fatalf("record=%#v", record)
	}
	for _, secret := range []string{"candidate-secret", "proxies:", "password:"} {
		if strings.Contains(output.String(), secret) {
			t.Fatal("configuration leaked into diagnostics")
		}
	}
}

func TestConfigDiagnostic_IOFailuresKeepPreviousState(t *testing.T) {
	for _, mode := range []string{"staging_directory", "staging_candidate", "read_previous", "publication"} {
		t.Run(mode, func(t *testing.T) {
			m, c, id := configDiagnosticManager(t)
			before := configDiagnosticRead(t, m.runtimeConfig)
			catalog := m.Subscriptions()
			snapshot := m.Snapshot()
			reloads := c.reloads
			message := ""
			switch mode {
			case "staging_directory":
				m.stagingDir = filepath.Join(m.runtimeConfig, "staging")
				message = "create subscription staging directory"
			case "staging_candidate":
				configDiagnosticReadOnly(t, m.stagingDir)
				message = "create generated configuration candidate"
			case "read_previous":
				if goruntime.GOOS == "windows" {
					t.Skip("requires Unix file permissions")
				}
				if err := os.Chmod(m.runtimeConfig, 0000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(m.runtimeConfig, 0600); err != nil {
						t.Error(err)
					}
				})
				message = "read previous runtime configuration"
			case "publication":
				configDiagnosticReadOnly(t, filepath.Dir(m.runtimeConfig))
				message = "install generated runtime configuration"
			}
			_, err := m.RefreshSubscription(context.Background(), Operation{ID: "io-failure", Source: "test"}, id)
			var api protocol.APIError
			var pathErr *os.PathError
			if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || api.Message != message || api.Details != nil {
				t.Fatalf("public IO error changed: %v", err)
			}
			if !errors.As(err, &pathErr) {
				t.Error("filesystem cause missing")
			}
			if mode == "read_previous" {
				if err := os.Chmod(m.runtimeConfig, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if c.reloads != reloads || !bytes.Equal(before, configDiagnosticRead(t, m.runtimeConfig)) || !reflect.DeepEqual(catalog, m.Subscriptions()) || !reflect.DeepEqual(snapshot, m.Snapshot()) {
				t.Fatal("failed preparation/publication changed valid state")
			}
		})
	}
}

func TestConfigDiagnostic_CatalogRestoreKeepsBothCauses(t *testing.T) {
	for _, mode := range []string{"use_apply", "remove_prepare", "remove_apply", "enabled_prepare", "enabled_apply"} {
		t.Run(mode, func(t *testing.T) {
			m, c, id := configDiagnosticManager(t)
			before := configDiagnosticRead(t, m.runtimeConfig)
			snapshot := m.Snapshot()
			first := &os.PathError{Op: "read", Path: "/private/config-secret", Err: os.ErrClosed}
			catalogDir := filepath.Dir(filepath.Dir(m.subscriptions.CachePath(id)))
			calls := 0
			if strings.HasSuffix(mode, "prepare") {
				m.validateConfig = func(context.Context, string) error {
					configDiagnosticReadOnly(t, catalogDir)
					return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo configuration validation failed"}, first)
				}
			} else {
				c.reload = func(context.Context) error {
					calls++
					if calls == 1 {
						configDiagnosticReadOnly(t, catalogDir)
						return first
					}
					return nil
				}
			}
			recorder := &coreDiagnosticRecorder{}
			m.diagnosticReporter = recorder.report
			op := Operation{ID: "catalog-restore", Source: "test"}
			var err error
			switch {
			case mode == "use_apply":
				_, err = m.UseSubscription(context.Background(), op, id)
			case strings.HasPrefix(mode, "remove"):
				err = m.RemoveSubscription(context.Background(), op, id)
			default:
				_, err = m.SetSubscriptionEnabled(context.Background(), op, id, false)
			}
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || api.Message != "subscription state rollback failed" || api.Details["degraded"] != true {
				t.Fatalf("restore contract changed: %v", err)
			}
			if !errors.Is(err, first) || !hasConfigDiagnosticPath(err, "open", "create") {
				t.Error("configuration or catalog restore cause missing")
			}
			if !bytes.Equal(before, configDiagnosticRead(t, m.runtimeConfig)) {
				t.Error("last valid config changed")
			}
			after := m.Snapshot()
			if after.Config.LastError != "generated configuration rollback could not be confirmed" || after.Revision != snapshot.Revision+1 {
				t.Error("degraded summary/revision changed")
			}
			records := recorder.snapshot()
			if len(records) != 1 || records[0].record.Event != "operation.failed" || !errors.Is(records[0].record.Err, first) {
				t.Error("missing single final diagnostic")
			}
		})
	}
}
