package logging

import (
	"io"
	"log/slog"
	"time"
)

// RFC3339Nano with a numeric zone offset (never "Z").
const rfc3339NanoNumeric = "2006-01-02T15:04:05.999999999-07:00"

// NewJSONHandler returns a slog JSON handler that stamps component, formats time
// as RFC3339Nano with a numeric offset, and redacts attrs through redactor.
func NewJSONHandler(out io.Writer, level *slog.LevelVar, component string, redactor *Redactor) slog.Handler {
	opts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.TimeKey {
				if ts, ok := attr.Value.Any().(time.Time); ok {
					return slog.String(slog.TimeKey, ts.Format(rfc3339NanoNumeric))
				}
			}
			if redactor == nil {
				return attr
			}
			return redactor.ReplaceAttr(groups, attr)
		},
	}
	return slog.NewJSONHandler(out, opts).WithAttrs([]slog.Attr{
		slog.String("component", component),
	})
}
