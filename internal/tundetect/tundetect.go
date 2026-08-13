// Package tundetect observes the system for other TUN-capable actors that could
// conflict with this daemon's managed mihomo TUN: other TUN network adapters
// (signal A) and other mihomo processes (signal B).
//
// The package only observes. Detect reports every TUN adapter and every mihomo
// process on the system without filtering; Classify then subtracts this daemon's
// own adapter/process using a flag supplied by the runtime layer (which knows
// mihomo's live TUN state). Keeping detection stateless lets it stay testable
// and free of runtime dependencies.
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

// Detection is the raw, unfiltered system observation. Neither field has this
// daemon's own adapter/process subtracted; that happens in Classify.
type Detection struct {
	// TunInterfaces lists every TUN adapter friendly name on the system
	// (Windows wintun, Linux tun, macOS utun).
	TunInterfaces []string
	// MihomoProcesses lists every mihomo process identifier on the system
	// (e.g. "mihomo.exe" or "mihomo (4321)").
	MihomoProcesses []string
}

// Backend is an injectable detection driver for tests and runtime wiring.
type Backend interface {
	Detect(ctx context.Context) (Detection, error)
}

// Detect runs platform detection via the default backend.
func Detect(ctx context.Context) (Detection, error) {
	return detect(ctx)
}
