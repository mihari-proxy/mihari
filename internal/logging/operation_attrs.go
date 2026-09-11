package logging

import "log/slog"

func validOperationLogID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == ':' {
			continue
		}
		return false
	}
	return true
}

func operationKey(key string) bool { return key == "operation_id" || key == "operation" }

// withoutOperationAttrs removes only attributes occupying reserved root keys.
// Empty-name groups are flattened by slog; named groups keep their children.
func withoutOperationAttrs(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if operationKey(attr.Key) {
			continue
		}
		if attr.Key == "" {
			value := attr.Value.Resolve()
			if value.Kind() == slog.KindGroup {
				children := withoutOperationAttrs(value.Group())
				if len(children) == 0 {
					continue
				}
				attr = slog.Attr{Value: slog.GroupValue(children...)}
			}
		}
		out = append(out, attr)
	}
	return out
}
