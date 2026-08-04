package ui

import "strings"

// MatchVisibleColumns reports whether query is a case-insensitive substring of
// any currently visible column value. Only keys listed in visible are searched;
// values present in cells but not in visible never contribute to a match.
// Empty or whitespace-only query matches everything.
func MatchVisibleColumns(cells map[string]string, visible []string, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	for _, key := range visible {
		value, ok := cells[key]
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}
