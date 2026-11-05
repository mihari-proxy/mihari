//go:build !windows

package sysproxy

import (
	"fmt"
	"os"
)

// ObservationScope binds a display observation to the process desktop context.
func (PlatformBackend) ObservationScope() (string, error) {
	return fmt.Sprintf("%d:%s:%s", os.Geteuid(), os.Getenv("DBUS_SESSION_BUS_ADDRESS"), os.Getenv("XDG_RUNTIME_DIR")), nil
}
