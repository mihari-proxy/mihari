package connections

import (
	"slices"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

const allSources = "All"

// matchesConnection applies the Source IP chip first, then case-insensitive
// substring search over currently visible column cell values only.
func matchesConnection(connection protocol.Connection, query, source string, visibleColumns []string) bool {
	if source != "" && source != allSources && connection.Metadata.SourceIP != source {
		return false
	}
	cells := make(map[string]string, len(visibleColumns))
	for _, column := range visibleColumns {
		cells[column] = columnValue(connection, column)
	}
	return ui.MatchVisibleColumns(cells, visibleColumns, query)
}

func sourceOptions(connections []protocol.Connection) []string {
	seen := make(map[string]struct{})
	for _, connection := range connections {
		if connection.Metadata.SourceIP != "" {
			seen[connection.Metadata.SourceIP] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen)+1)
	result = append(result, allSources)
	for source := range seen {
		result = append(result, source)
	}
	slices.Sort(result[1:])
	return result
}

func validSource(source string, options []string) string {
	if slices.Contains(options, source) {
		return source
	}
	return allSources
}
