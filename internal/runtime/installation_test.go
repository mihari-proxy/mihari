package runtime

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
)

func TestInstallationRuntime_ReadOnlyInspector(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	m := New(Options{InstallationStatus: func(got context.Context) (protocol.InstallationStatus, error) {
		if got != ctx {
			t.Error("lost inspection context")
		}
		calls++
		return protocol.InstallationStatus{Schema: "mihari.install-status/v1", Kind: "permission_required", ServiceState: "unknown", Reason: "permission_required"}, nil
	}})
	status, err := m.GetInstallationStatus(ctx)
	if err != nil || status.Kind != "permission_required" || calls != 1 {
		t.Fatalf("kind=%s calls=%d err=%v", status.Kind, calls, err)
	}
	status, err = New(Options{}).GetInstallationStatus(ctx)
	if err != nil || status.Kind != "unknown" {
		t.Fatal("missing inspector fabricated installation state")
	}
}
