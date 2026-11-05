//go:build !windows

package sysproxy

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestObservationScope_DesktopIdentity(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/synthetic/bus-a")
	t.Setenv("XDG_RUNTIME_DIR", "/synthetic/runtime-a")
	backend := PlatformBackend{}
	initial, err := backend.ObservationScope()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(initial, strconv.Itoa(os.Geteuid())+":") {
		t.Fatalf("scope lacks euid: %q", initial)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/synthetic/bus-b")
	bus, err := backend.ObservationScope()
	if err != nil || initial == bus {
		t.Fatalf("bus change ignored: %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", "/synthetic/runtime-b")
	runtime, err := backend.ObservationScope()
	if err != nil || runtime == bus {
		t.Fatalf("runtime change ignored: %v", err)
	}
}
