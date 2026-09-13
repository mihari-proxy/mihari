package app

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/platform"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestPortRecovery_PreservesLatestSettingsAndRejectsCompletion(t *testing.T) {
	dir := t.TempDir()
	paths := platform.Paths{Settings: filepath.Join(dir, "settings.yaml"), Onboarding: filepath.Join(dir, "onboarding.json")}
	initial := config.Defaults()
	initial.ControllerSecret = strings.Repeat("ab", 32)
	latest := initial
	latest.MixedAddr = "127.0.0.1:19290"
	if err := config.Save(paths.Settings, latest); err != nil {
		t.Fatal(err)
	}
	r, err := NewPortRecovery(paths, initial, state.NewStore(state.Snapshot{}), &ManagedPortConflict{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	port := "127.0.0.1:19291"
	if _, err := r.UpdateOnboarding(context.Background(), runtimeapi.Operation{ID: "ports"}, onboarding.Update{WebAddr: &port}); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(paths.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if saved.MixedAddr != latest.MixedAddr || saved.WebAddr != port {
		t.Fatal("recovery overwrote newer settings or failed to save ports")
	}
	complete := true
	if _, err := r.UpdateOnboarding(context.Background(), runtimeapi.Operation{ID: "finish"}, onboarding.Update{Complete: &complete}); err == nil {
		t.Fatal("restricted recovery completed setup")
	}
}

func TestPortConflict_IsDistinguishableFromPermissionFailure(t *testing.T) {
	for _, test := range []struct {
		name  string
		cause error
		want  bool
	}{
		{"occupied", syscall.EADDRINUSE, true}, {"permission", syscall.EACCES, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := probeManagedPortsWithListener(config.Defaults(), nil, func(string, string) (net.Listener, error) { return nil, test.cause })
			var conflict interface{ PortConflict() bool }
			got := errors.As(err, &conflict) && conflict.PortConflict()
			if got != test.want {
				t.Fatalf("port recovery eligibility=%v want=%v", got, test.want)
			}
		})
	}
}
