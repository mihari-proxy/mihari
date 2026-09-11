package runtime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestTunDiagnostic_MapPreservesCausesAndReportedResult(t *testing.T) {
	cause := &os.PathError{Op: "open", Path: "/private/business-secret", Err: os.ErrPermission}
	for _, degraded := range []bool{false, true} {
		api := protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "configuration rejected"}
		if degraded {
			api.Details = map[string]any{"degraded": true}
		}
		reported := diagnostics.MarkReported(diagnostics.Wrap(api, cause))
		mapped := mapTunApplyError(reported)
		if !errors.Is(mapped, cause) || !diagnostics.AlreadyReported(mapped) || mapped.Error() != reported.Error() {
			t.Fatalf("same result lost cause or marker: %v", mapped)
		}
	}
	for _, input := range []error{cause, diagnostics.Wrap(protocol.APIError{Code: protocol.CodePermissionDenied, Message: "rejected"}, cause)} {
		mapped := mapTunApplyError(input)
		var api protocol.APIError
		if !errors.Is(mapped, cause) || !errors.As(mapped, &api) || api.Code != protocol.CodePermissionDenied {
			t.Fatalf("permission reclassification lost cause: %v", mapped)
		}
	}
	other := errors.New("raw rejection")
	if !errors.Is(mapTunApplyError(other), other) {
		t.Fatal("fallback lost cause")
	}
}

func TestTunDiagnostic_RuntimeFailureMatrixJSON(t *testing.T) {
	for _, stage := range []string{"pre_live", "patch", "confirmation", "settings_restore", "live_restore", "save"} {
		t.Run(stage, func(t *testing.T) {
			cause := &os.PathError{Op: "open", Path: "/private/business-secret", Err: os.ErrPermission}
			restore := &os.PathError{Op: "rename", Path: "/private/business-secret", Err: os.ErrExist}
			failure := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "configuration rejected"}, cause)
			c := &fakeController{configs: map[string]any{"tun": map[string]any{"enable": false, "stack": "system"}}}
			c.configsFunc = func(context.Context) (map[string]any, error) {
				if stage == "pre_live" || (stage == "confirmation" && c.patchCalls == 1) {
					return nil, cause
				}
				return c.configs, nil
			}
			c.patchConfigs = func(_ context.Context, patch map[string]any) error {
				if c.patchCalls == 1 && stage != "confirmation" && stage != "pre_live" && stage != "save" {
					return failure
				}
				if stage == "live_restore" && c.patchCalls == 2 {
					return restore
				}
				c.configs["tun"] = cloneTunMap(patch["tun"].(map[string]any))
				return nil
			}
			var output bytes.Buffer
			saves := 0
			m := newTestManager(Options{Controller: c, Settings: defaultTunSettings(nil), SettingsPath: "settings.yaml", DiagnosticReporter: businessJSONReporter(&output), SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
				saves++
				if stage == "save" {
					return config.CommitResult{}, cause
				}
				if stage == "settings_restore" && saves == 2 {
					return config.CommitResult{}, restore
				}
				return config.CommitResult{Committed: true}, nil
			}})
			op := Operation{ID: "tun-" + stage, Source: "test"}
			_, err := m.EnableTun(context.Background(), op, true)
			if !errors.Is(err, cause) {
				t.Fatalf("original cause lost: %v", err)
			}
			degraded := stage == "settings_restore" || stage == "live_restore"
			if degraded && !errors.Is(err, restore) {
				t.Fatal("compensation cause lost")
			}
			code := "upstream_failure"
			if degraded || stage == "save" {
				code = "data_failure"
			}
			if _, replay := m.EnableTun(context.Background(), op, true); !errors.Is(replay, cause) {
				t.Fatal("replay lost cause")
			}
			assertBusinessFailureJSON(t, output.String(), op.ID, "tun.enable", code)
			if degraded != (m.Snapshot().Health == "degraded") {
				t.Fatalf("degraded state changed: %#v", m.Snapshot())
			}
		})
	}
}
