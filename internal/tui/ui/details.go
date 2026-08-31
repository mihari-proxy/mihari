package ui

// DetailStrings reads a []string field from protocol APIError Details.
// It accepts both in-process []string and JSON-decoded []any so TUN-conflict
// interface names survive HTTP round-trips.
func DetailStrings(details map[string]any, key string) []string {
	if details == nil {
		return nil
	}
	value, ok := details[key]
	if !ok || value == nil {
		return nil
	}
	switch slice := value.(type) {
	case []string:
		return slice
	case []any:
		out := make([]string, 0, len(slice))
		for _, item := range slice {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}
