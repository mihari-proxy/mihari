package core_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

func TestConfigDiagnostic_TrustedCompensationFailures(t *testing.T) {
	for _, mode := range []string{"restore_write", "first_path", "restored_path", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			var records []diagnostics.Record
			m, f, c, service := seamManager(t, func(o *runtimeapi.Options) {
				o.DiagnosticReporter = func(ctx context.Context, r diagnostics.Record) {
					op, _ := logging.OperationFromContext(ctx)
					if op.ID != "trusted-config" {
						t.Error("operation ID lost")
					}
					records = append(records, r)
				}
			})
			id := cachedProfile(t, service)
			before := f.Content()
			snapshot := m.Snapshot()
			first := &os.PathError{Op: "open", Path: "/private/first-secret", Err: os.ErrPermission}
			second := &os.PathError{Op: "read", Path: "/private/restore-secret", Err: os.ErrClosed}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "first_path" {
				f.FailConfigPathAfter(1, first)
			}
			if mode == "restore_write" {
				f.FailConfigWriteAfter(2, second)
			}
			if mode == "restored_path" {
				f.FailConfigPathAfter(2, second)
			}
			c.reload = func(got context.Context) error {
				if mode == "first_path" {
					return nil
				}
				if c.reloads == 1 {
					if mode == "cancel" {
						cancel()
					}
					return first
				}
				if got.Err() != nil {
					t.Error("trusted compensation retained canceled context")
				}
				return nil
			}
			_, err := m.UseSubscription(ctx, runtimeapi.Operation{ID: "trusted-config", Source: "test"}, id)
			var api protocol.APIError
			degraded := mode == "restore_write" || mode == "restored_path"
			if !errors.As(err, &api) || api.Code != protocol.CodeUpstreamFailure || (api.Details["degraded"] == true) != degraded {
				t.Fatalf("trusted contract changed: %v", err)
			}
			if !errors.Is(err, first) || (degraded && !errors.Is(err, second)) {
				t.Error("trusted original/recovery cause missing")
			}
			wantReloads := 2
			if mode != "cancel" {
				wantReloads = 1
			}
			if c.reloads != wantReloads || !bytes.Equal(before, f.Content()) {
				t.Error("trusted restore/reload ordering changed")
			}
			after := m.Snapshot()
			if degraded {
				if after.Config.LastError != "generated configuration rollback could not be confirmed" || after.Revision != snapshot.Revision+1 {
					t.Error("degraded summary/revision changed")
				}
			} else if after.Revision != snapshot.Revision {
				t.Error("successful compensation changed revision")
			}
			if len(records) != 1 || records[0].Event != "operation.failed" || records[0].Level != slog.LevelError || !errors.Is(records[0].Err, first) {
				t.Error("missing single final diagnostic")
			}
		})
	}
}
