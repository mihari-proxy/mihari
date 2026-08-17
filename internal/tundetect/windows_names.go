package tundetect

import "strings"

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
