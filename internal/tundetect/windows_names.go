package tundetect

import "strings"

// IfOperStatus values from iptypes.h, duplicated so this file stays
// OS-agnostic and unit-testable without golang.org/x/sys/windows.
const (
	ifOperStatusUp             = 1
	ifOperStatusDown           = 2
	ifOperStatusTesting        = 3
	ifOperStatusUnknown        = 4
	ifOperStatusDormant        = 5
	ifOperStatusNotPresent     = 6
	ifOperStatusLowerLayerDown = 7
)

// windowsTunCandidate is one GetAdaptersAddresses row before name/status
// classification. OperStatus is IfOperStatus.
type windowsTunCandidate struct {
	desc       string
	friendly   string
	operStatus uint32
}

func isWindowsTunUp(operStatus uint32) bool {
	return operStatus == ifOperStatusUp
}

func collectWindowsTunNames(candidates []windowsTunCandidate) []string {
	var adapters []string
	for _, candidate := range candidates {
		if !isWindowsTunUp(candidate.operStatus) {
			continue
		}
		if !isWindowsTunAdapter(candidate.desc, candidate.friendly) {
			continue
		}
		adapters = append(adapters, formatAdapterName(candidate.desc, candidate.friendly))
	}
	return adapters
}

func isWindowsTunAdapter(desc, friendly string) bool {
	haystack := strings.ToLower(desc + " " + friendly)
	for _, needle := range []string{"wintun", "meta tunnel", "wireguard"} {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	name := strings.ToLower(strings.TrimSpace(friendly))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(desc))
	}
	return name == "mihomo" || name == "meta"
}

func formatAdapterName(desc, friendly string) string {
	friendly = strings.TrimSpace(friendly)
	desc = strings.TrimSpace(desc)
	switch {
	case friendly == "":
		return desc
	case desc == "" || strings.EqualFold(friendly, desc):
		return friendly
	default:
		return friendly + " (" + desc + ")"
	}
}
