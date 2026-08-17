package tundetect

import (
	"fmt"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// Classify derives conflict evidence from a raw Detection by subtracting this
// daemon's own TUN adapter and own mihomo process as identified by self. It
// returns nil when no other actor remains, which the runtime treats as "no
// conflict".
//
// Adapter subtraction is gated on self.TunActive: the daemon occupies a TUN
// adapter only when mihomo's live tun.enable is true. When TunName is known,
// the matching listed name is dropped; when it is empty, exactly one entry
// is dropped (name unknown, so the count is conservative). Process
// subtraction matches self.CorePID; a zero PID means identity is unknown
// and nothing is dropped (signal B does not gate enable).
func Classify(d Detection, self Self) *protocol.TunConflict {
	otherTun := subtractSelfTun(d.TunInterfaces, self)
	otherMihomo := formatOtherProcesses(d.MihomoProcesses, self.CorePID)
	if len(otherTun) == 0 && len(otherMihomo) == 0 {
		return nil
	}
	return &protocol.TunConflict{
		OtherTunInterfaces:   otherTun,
		OtherMihomoProcesses: otherMihomo,
	}
}

func subtractSelfTun(ifaces []string, self Self) []string {
	if !self.TunActive || len(ifaces) == 0 {
		return ifaces
	}
	if self.TunName == "" {
		return ifaces[1:]
	}
	for i, name := range ifaces {
		if adapterNameMatch(name, self.TunName) {
			out := make([]string, 0, len(ifaces)-1)
			out = append(out, ifaces[:i]...)
			out = append(out, ifaces[i+1:]...)
			return out
		}
	}
	return ifaces
}

func formatOtherProcesses(procs []Process, corePID int) []string {
	var out []string
	dropped := false
	for _, p := range procs {
		if !dropped && corePID != 0 && p.PID == corePID {
			dropped = true
			continue
		}
		out = append(out, formatProcess(p))
	}
	return out
}

func formatProcess(p Process) string {
	return fmt.Sprintf("%s (%d)", p.Name, p.PID)
}

// adapterNameMatch reports whether a listed adapter display name refers to
// tunName. Comparison is case-insensitive and accepts formatAdapterName
// output: exact equality, "tunName (desc)", or "alias (tunName)".
func adapterNameMatch(listed, tunName string) bool {
	listed = strings.TrimSpace(listed)
	tunName = strings.TrimSpace(tunName)
	if tunName == "" {
		return false
	}
	l := strings.ToLower(listed)
	n := strings.ToLower(tunName)
	if l == n {
		return true
	}
	if strings.HasPrefix(l, n+" (") && strings.HasSuffix(l, ")") {
		return true
	}
	return strings.HasSuffix(l, " ("+n+")")
}
