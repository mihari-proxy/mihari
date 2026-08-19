// Package tundetect observes the system for other TUN-capable actors that could
// conflict with this daemon's managed mihomo TUN: other TUN network adapters
// (signal A) and other mihomo processes (signal B).
//
// The package only observes. Detect reports TUN adapters that can currently
// take routes (Windows: IfOperStatusUp only; leftover Down Wintun devices are
// ignored) and every mihomo process on the system. Classify then subtracts this
// daemon's own adapter/process using a Self identity supplied by the runtime
// layer (live tun.enable / tun.device, supervised core PID, controller
// occupant, parent PID, and managed binary path). Keeping detection
// stateless lets it stay testable and free of runtime dependencies.
//
// Platform backends implement unexported detect:
//   - Windows: IP adapter table (wintun) + toolhelp process snapshot
//   - Linux: /sys/class/net tun flags + /proc/*/comm
//   - macOS: ifconfig utun + ps
//
// Detection is best-effort: a detect error is surfaced but never blocks TUN
// enable on its own (the runtime treats a failed detection as "no evidence").
package tundetect

import "context"

// Process is one observed mihomo process. Classify subtracts by identity
// (supervised PID, controller occupant, parent PID, or managed binary path)
// and formats the remainder as "name (pid)" for the protocol layer.
type Process struct {
	Name      string
	PID       int
	ParentPID int
	Path      string
}

// Self is this daemon's identity used by Classify to subtract our own
// adapter and process from a raw Detection.
type Self struct {
	// TunActive is true when this daemon's mihomo has live tun.enable.
	TunActive bool
	// TunName is live tun.device when known; empty means unknown.
	TunName string
	// CorePID is the supervised mihomo PID; zero means unknown.
	CorePID int
	// OccupantPID is the process listening on this daemon's controller address.
	OccupantPID int
	// DaemonPID is this daemon process; a mihomo whose parent matches is self.
	DaemonPID int
	// BinaryPath is the managed core executable; a matching image path is self.
	BinaryPath string
}

// Detection is the system observation after dropping adapters that cannot
// take routes. Neither field has this daemon's own adapter/process
// subtracted; that happens in Classify.
type Detection struct {
	// TunInterfaces lists TUN adapters that can currently take routes
	// (Windows wintun/Meta/WireGuard with IfOperStatusUp; Linux tun; macOS utun).
	TunInterfaces []string
	// MihomoProcesses lists every mihomo process on the system.
	MihomoProcesses []Process
}

// Backend is an injectable detection driver for tests and runtime wiring.
type Backend interface {
	Detect(ctx context.Context) (Detection, error)
}

// Detect runs platform detection via the default backend.
func Detect(ctx context.Context) (Detection, error) {
	return detect(ctx)
}
