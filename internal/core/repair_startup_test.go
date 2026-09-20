package core_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
)

func TestRootAssembly_FailedCoreValidationRetainsRepairOwner(t *testing.T) {
	f, paths, _, _ := startupSourceFixture(t)
	settings, portProbe := startupSettings(t)
	previous := append([]byte(nil), f.Content()...)
	failure := errors.New("local core cannot execute")
	calls := 0
	f.Execute = func(_ context.Context, command core.CoreCommand) ([]byte, error) {
		calls++
		if command.Args[0] != "-t" {
			t.Fatalf("unexpected core execution: %v", command.Args)
		}
		return nil, failure
	}
	assembly, err := app.BuildRuntimeWithOptions(paths, settings, "test", nil, nil, app.RuntimeBuildOptions{TrustedCore: f.Trusted, PortProbeListen: portProbe})
	if err != nil {
		t.Fatal(err)
	}
	if assembly.Manager == nil || assembly.Store.Load().Health != "degraded" || calls != 1 {
		t.Fatalf("missing repair control plane: state=%+v calls=%d", assembly.Store.Load(), calls)
	}
	if !bytes.Equal(previous, f.Content()) {
		t.Fatal("repair startup rewrote the previous configuration")
	}
	found := false
	for _, capability := range assembly.Manager.Capabilities() {
		found = found || capability == protocol.CapabilityCoreReinstall
	}
	if !found {
		t.Fatal("repair capability unavailable")
	}
}
