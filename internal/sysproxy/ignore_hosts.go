package sysproxy

import "strings"

// ignoreHostsArray renders defaultBypass as a GVariant string array literal, the
// form gsettings expects for the ignore-hosts key.
func ignoreHostsArray() string {
	quoted := make([]string, len(defaultBypass))
	for i, h := range defaultBypass {
		quoted[i] = "'" + h + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
