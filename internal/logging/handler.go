package logging

import (
	"context"
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
			return attr
		},
	}
	return &redactingHandler{next: slog.NewJSONHandler(out, opts), redactor: redactor, component: component}
}

type redactingHandler struct {
	next      slog.Handler
	redactor  *Redactor
	component string
	groups    []string
	ops       []handlerOp
}

type handlerOp struct {
	group string
	attrs []slog.Attr
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	msg := record.Message
	if h.redactor != nil {
		msg = h.redactor.String(msg)
	}
	clean := slog.NewRecord(record.Time, record.Level, msg, record.PC)
	component := h.component
	attrs := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		attr, component = h.extractTopLevelComponent(attr, component)
		if attr = h.cleanAttr(attr); !attr.Equal(slog.Attr{}) {
			attrs = append(attrs, attr)
		}
		return true
	})
	clean.AddAttrs(attrs...)
	next := h.next.WithAttrs([]slog.Attr{slog.String("component", component)})
	for _, op := range h.ops {
		if op.group != "" {
			next = next.WithGroup(op.group)
		} else {
			next = next.WithAttrs(op.attrs)
		}
	}
	return next.Handle(ctx, clean)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attrs))
	component := h.component
	for _, attr := range attrs {
		attr, component = h.extractTopLevelComponent(attr, component)
		if attr = h.cleanAttr(attr); !attr.Equal(slog.Attr{}) {
			clean = append(clean, attr)
		}
	}
	ops := append([]handlerOp{}, h.ops...)
	if len(clean) > 0 {
		ops = append(ops, handlerOp{attrs: clean})
	}
	return &redactingHandler{next: h.next, redactor: h.redactor, component: component, groups: h.groups, ops: ops}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := append(append([]string{}, h.groups...), name)
	ops := append(append([]handlerOp{}, h.ops...), handlerOp{group: name})
	return &redactingHandler{next: h.next, redactor: h.redactor, component: h.component, groups: groups, ops: ops}
}

func (h *redactingHandler) cleanAttr(attr slog.Attr) slog.Attr {
	if h.redactor != nil {
		return h.redactor.ReplaceAttr(h.groups, attr)
	}
	return attr
}

func (h *redactingHandler) extractTopLevelComponent(attr slog.Attr, component string) (slog.Attr, string) {
	if len(h.groups) != 0 {
		return attr, component
	}
	value := attr.Value.Resolve()
	if attr.Key == "component" {
		if value.Kind() == slog.KindString {
			component = value.String()
			if h.redactor != nil {
				component = h.redactor.String(component)
			}
		}
		return slog.Attr{}, component
	}
	if attr.Key != "" || value.Kind() != slog.KindGroup {
		return attr, component
	}

	children := value.Group()
	clean := make([]slog.Attr, 0, len(children))
	for _, child := range children {
		child, component = h.extractTopLevelComponent(child, component)
		if !child.Equal(slog.Attr{}) {
			clean = append(clean, child)
		}
	}
	if len(clean) == 0 {
		return slog.Attr{}, component
	}
	return slog.Attr{Value: slog.GroupValue(clean...)}, component
}
