package tundetect

import (
	"fmt"
	"path/filepath"
	"runtime"
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
// subtraction treats a process as self when any identity matches: CorePID,
// controller OccupantPID, parent==DaemonPID, or managed BinaryPath. An empty
// identity drops nothing (signal B does not gate enable).
func Classify(d Detection, self Self) *protocol.TunConflict {
	otherTun := subtractSelfTun(d.TunInterfaces, self)
	otherMihomo := formatOtherProcesses(d.MihomoProcesses, self)
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

func formatOtherProcesses(procs []Process, self Self) []string {
	var out []string
	for _, p := range procs {
		if isSelfProcess(p, self) {
			continue
		}
		out = append(out, formatProcess(p))
	}
	return out
}

func isSelfProcess(p Process, self Self) bool {
	if p.PID <= 0 {
		return false
	}
	if self.OccupantPID != 0 && p.PID == self.OccupantPID {
		return true
	}
	if self.DaemonPID != 0 && p.ParentPID != 0 && p.ParentPID == self.DaemonPID {
		return true
	}
	if binaryPathMatch(p.Path, self.BinaryPath) {
		return true
	}
	if self.CorePID == 0 || p.PID != self.CorePID {
		return false
	}
	// A stored core PID may be stale and reused by a foreign process. When
	// live identities are available, they must not contradict this process.
	if self.OccupantPID != 0 && p.PID != self.OccupantPID {
		return false
	}
	if self.DaemonPID != 0 && p.ParentPID != 0 && p.ParentPID != self.DaemonPID {
		return false
	}
	if self.BinaryPath != "" && p.Path != "" && !binaryPathMatch(p.Path, self.BinaryPath) {
		return false
	}
	return true
}

func binaryPathMatch(listed, want string) bool {
	listed = strings.TrimSpace(listed)
	want = strings.TrimSpace(want)
	if listed == "" || want == "" {
		return false
	}
	cleanListed, cleanWant := filepath.Clean(listed), filepath.Clean(want)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(cleanListed, cleanWant)
	}
	return cleanListed == cleanWant
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
