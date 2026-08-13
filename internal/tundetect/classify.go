package tundetect

import "github.com/mihari-proxy/mihari/internal/control/protocol"

// Classify derives conflict evidence from a raw Detection by subtracting this
// daemon's own TUN adapter (when selfTunActive) and own mihomo process. It
// returns nil when no other actor remains, which the runtime treats as "no
// conflict".
//
// Subtraction is positional and best-effort: detection lists are unordered, so
// one entry is dropped rather than matched by identity. Only the count of
// *other* actors matters for the gate; on a tie this errs toward caution (it
// can only over-count by the very own adapter/process we own).
//
// TunInterfaces subtraction is gated on selfTunActive: the daemon contributes
// its own TUN adapter only when mihomo's live tun.enable is true. MihomoProcesses
// subtraction is unconditional: this daemon always drives at most one mihomo,
// regardless of TUN state, so that process is always self.
func Classify(d Detection, selfTunActive bool) *protocol.TunConflict {
	otherTun := d.TunInterfaces
	if selfTunActive && len(otherTun) > 0 {
		otherTun = otherTun[1:]
	}
	otherMihomo := d.MihomoProcesses
	if len(otherMihomo) > 0 {
		otherMihomo = otherMihomo[1:]
	}
	if len(otherTun) == 0 && len(otherMihomo) == 0 {
		return nil
	}
	return &protocol.TunConflict{
		OtherTunInterfaces:   otherTun,
		OtherMihomoProcesses: otherMihomo,
	}
}
