package update

import "strings"

// ReplacementRisk describes compatibility risk when replacing installed bytes.
type ReplacementRisk string

const (
	// ReplacementNone means no known compatibility risk.
	ReplacementNone ReplacementRisk = "none"
	// ReplacementDowngrade means at least one installed version is newer.
	ReplacementDowngrade ReplacementRisk = "downgrade"
	// ReplacementUnknown means compatibility cannot be determined.
	ReplacementUnknown ReplacementRisk = "unknown"
)

// ClassifyReplacementVersion compares installed and candidate versions.
func ClassifyReplacementVersion(current, target string) ReplacementRisk {
	normalize := func(value string) string {
		value = strings.TrimSpace(value)
		if value != "" && !strings.HasPrefix(value, "v") {
			value = "v" + value
		}
		return value
	}
	order, ok := compareCanonicalTags(normalize(current), normalize(target))
	if !ok {
		return ReplacementUnknown
	}
	if order > 0 {
		return ReplacementDowngrade
	}
	return ReplacementNone
}
