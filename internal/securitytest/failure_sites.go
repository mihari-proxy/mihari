//go:build unix_security && (linux || darwin)

package securitytest

import (
	"regexp"
	"strings"
)

var childFailureSite = regexp.MustCompile(`(?m)^[ \t]*([A-Za-z_][A-Za-z0-9_]{0,100}_test\.go:[0-9]{1,6}:)`)

// ChildFailureSites extracts only bounded test-source coordinates from child
// output. It never forwards failure messages, paths, credentials or result data.
func ChildFailureSites(output []byte) string {
	if len(output) > 65536 {
		output = output[:65536]
	}
	var sites []string
	seen := make(map[string]bool)
	for _, match := range childFailureSite.FindAllSubmatch(output, -1) {
		site := string(match[1])
		if seen[site] {
			continue
		}
		seen[site] = true
		sites = append(sites, site)
		if len(sites) == 16 {
			break
		}
	}
	return strings.Join(sites, "\n")
}
