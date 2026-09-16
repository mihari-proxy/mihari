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
// as RFC3339Nano with a numeric offset, and preserves original log content.
// Terminal control escaping is owned separately by terminal display adapters.
func NewJSONHandler(out io.Writer, level *slog.LevelVar, component string, _ *Redactor) slog.Handler {
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
	return &contextHandler{next: slog.NewJSONHandler(&recordWriter{out: out}, opts), level: level, component: component}
}

type contextHandler struct {
	next      slog.Handler
	level     *slog.LevelVar
	component string
	groups    []string
	ops       []handlerOp
}

type handlerOp struct {
	group string
	attrs []slog.Attr
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return (h.level == nil || h.level.Level() != LevelSilent) && h.next.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	operation, bound := OperationFromContext(ctx)
	msg := record.Message
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
	hiddenRootGroup := bound && len(h.groups) > 0 && operationKey(h.groups[0])
	if hiddenRootGroup {
		attrs = nil
	} else if bound && len(h.groups) == 0 {
		attrs = withoutOperationAttrs(attrs)
	}
	clean.AddAttrs(attrs...)

	root := []slog.Attr{slog.String("component", component)}
	if bound {
		if validOperationLogID(operation.ID) {
			root = append(root, slog.String("operation_id", operation.ID))
		}
		if operation.Name != "" {
			root = append(root, slog.String("operation", operation.Name))
		}
	}
	// Context metadata belongs to the root, not to h.groups.
	next := h.next.WithAttrs(root)
	grouped := false
	for _, op := range h.ops {
		if op.group != "" {
			if bound && !grouped && operationKey(op.group) {
				break
			}
			next = next.WithGroup(op.group)
			grouped = true
		} else {
			opAttrs := op.attrs
			if bound && !grouped {
				opAttrs = withoutOperationAttrs(opAttrs)
			}
			next = next.WithAttrs(opAttrs)
		}
	}
	return next.Handle(ctx, clean)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
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
	return &contextHandler{next: h.next, level: h.level, component: component, groups: h.groups, ops: ops}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := append(append([]string{}, h.groups...), name)
	ops := append(append([]handlerOp{}, h.ops...), handlerOp{group: name})
	return &contextHandler{next: h.next, level: h.level, component: h.component, groups: groups, ops: ops}
}

func (h *contextHandler) cleanAttr(attr slog.Attr) slog.Attr {
	attr.Value = attr.Value.Resolve()
	if attr.Value.Kind() == slog.KindAny {
		if err, ok := attr.Value.Any().(error); ok {
			return slog.String(attr.Key, diagnosticText(err, nil))
		}
	}
	if attr.Value.Kind() == slog.KindGroup {
		children := attr.Value.Group()
		resolved := make([]slog.Attr, len(children))
		for i, child := range children {
			resolved[i] = h.cleanAttr(child)
		}
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(resolved...)}
	}
	return attr
}

func (h *contextHandler) extractTopLevelComponent(attr slog.Attr, component string) (slog.Attr, string) {
	if len(h.groups) != 0 {
		return attr, component
	}
	value := attr.Value.Resolve()
	if attr.Key == "component" {
		if value.Kind() == slog.KindString {
			component = value.String()
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
