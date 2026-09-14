package update

import "strings"

// safeUnrecognizedVersion retains a bounded build identifier, never arbitrary
// process output. Only ordinary surrounding spaces may be discarded.
func safeUnrecognizedVersion(value string) string {
	value = strings.Trim(value, " ")
	if len(value) == 0 || len(value) > 128 || normalizedReplacementVersion(value) != "" {
		return ""
	}
	for _, c := range value {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._+-", c) {
			continue
		}
		return ""
	}
	return value
}
