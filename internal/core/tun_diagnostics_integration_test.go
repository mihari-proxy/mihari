package core_test

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
	"github.com/mihari-proxy/mihari/internal/logging"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

type diagnosticTunController struct {
	*tunFaultController
	first, restore error
	preLive        bool
}

func (c *diagnosticTunController) Configs(ctx context.Context) (map[string]any, error) {
	if c.preLive || c.reloads == 1 {
		return nil, c.first
	}
	if c.reloads > 1 && c.restore != nil {
		return nil, c.restore
	}
	return c.tunFaultController.Configs(ctx)
}
func TestTunDiagnostic_TrustedConfirmationAndRollbackJSON(t *testing.T) {
	for _, stage := range []string{"pre_live", "confirmation", "restore_confirmation", "restore_settings"} {
		t.Run(stage, func(t *testing.T) {
			first := &os.PathError{Op: "open", Path: "/private/business-secret", Err: os.ErrPermission}
			second := &os.PathError{Op: "rename", Path: "/private/business-secret", Err: os.ErrExist}
			var output bytes.Buffer
			level := new(slog.LevelVar)
			level.Set(slog.LevelDebug)
			redactor := logging.NewRedactor("business-secret")
			var c *diagnosticTunController
			saves := 0
			m, f, _, _ := seamManager(t, func(o *runtimeapi.Options) {
				c = &diagnosticTunController{tunFaultController: &tunFaultController{seamController: o.Controller.(*seamController)}, first: first, preLive: stage == "pre_live"}
				if stage == "restore_confirmation" {
					c.restore = second
				}
				o.Controller = c
				o.SettingsPath = "settings.yaml"
				o.SaveSettings = func(string, config.Settings) (config.CommitResult, error) {
					saves++
					if stage == "restore_settings" && saves == 2 {
						return config.CommitResult{}, second
					}
					return config.CommitResult{Committed: true}, nil
				}
				o.DiagnosticReporter = logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, level, "daemon", redactor)), redactor)
			})
			before := f.Content()
			op := runtimeapi.Operation{ID: "trusted-tun-" + stage, Source: "test"}
			_, err := m.EnableTun(context.Background(), op, true)
			if !errors.Is(err, first) {
				t.Fatalf("trusted confirmation cause lost: %v", err)
			}
			degraded := stage == "restore_confirmation" || stage == "restore_settings"
			if degraded && !errors.Is(err, second) {
				t.Fatal("trusted restore cause lost")
			}
			var api protocol.APIError
			if !errors.As(err, &api) || (degraded && api.Code != protocol.CodeDataFailure) || (!degraded && api.Code != protocol.CodeUpstreamFailure) {
				t.Fatalf("public error changed: %v", err)
			}
			if _, replay := m.EnableTun(context.Background(), op, true); !errors.Is(replay, first) {
				t.Fatal("replay cause lost")
			}
			var record map[string]any
			if e := json.Unmarshal(output.Bytes(), &record); e != nil {
				t.Fatalf("expected one diagnostic: %v", e)
			}
			if record["operation_id"] != op.ID || record["operation"] != "tun.enable" || record["level"] != "ERROR" || !strings.Contains(output.String(), "permission denied") || strings.Contains(output.String(), "business-secret") {
				t.Fatalf("record=%#v", record)
			}
			if !bytes.Equal(before, f.Content()) || c.patches != 0 {
				t.Fatal("trusted compensation config/PATCH contract changed")
			}
		})
	}
}
