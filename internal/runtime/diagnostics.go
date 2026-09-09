package runtime

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

type operationDiagnosticsContextKey struct{}

type operationDiagnostics struct {
	mu      sync.Mutex
	records []diagnostics.Record
}

func newOperationDiagnostics(ctx context.Context, key string) (context.Context, *operationDiagnostics) {
	if ctx == nil {
		ctx = context.Background()
	}
	batch := &operationDiagnostics{}
	ctx = context.WithValue(ctx, operationDiagnosticsContextKey{}, batch)
	prefix, id, found := strings.Cut(key, ":")
	if found && id != "" {
		if name, ok := operationDiagnosticNames[prefix]; ok {
			ctx = logging.WithOperation(ctx, logging.OperationMetadata{ID: id, Name: name})
		}
	}
	return ctx, batch
}

func collectWarning(ctx context.Context, component, event string, err error) {
	if err == nil || ctx == nil {
		return
	}
	batch, ok := ctx.Value(operationDiagnosticsContextKey{}).(*operationDiagnostics)
	if !ok || batch == nil {
		return
	}
	batch.mu.Lock()
	batch.records = append(batch.records, diagnostics.Record{Component: component, Event: event, Level: slog.LevelWarn, Err: err})
	batch.mu.Unlock()
}

func (m *Manager) flushDiagnostics(ctx context.Context, batch *operationDiagnostics) {
	if m.diagnosticReporter == nil || batch == nil {
		return
	}
	batch.mu.Lock()
	records := append([]diagnostics.Record(nil), batch.records...)
	batch.mu.Unlock()
	for _, record := range records {
		m.diagnosticReporter(ctx, record)
	}
}

func operationSuccessKey(key string) bool {
	prefix, _, found := strings.Cut(key, ":")
	if !found {
		return false
	}
	switch prefix {
	case "install", "restart", "sub-add", "sub-refresh", "sub-remove", "sub-set", "sub-enabled", "sub-use":
		return true
	default:
		return false
	}
}

var operationDiagnosticNames = map[string]string{
	"close":            "connection.close",
	"close-all":        "connection.close_all",
	"geoip":            "geoip.update",
	"install":          "core.install",
	"logging":          "logging.update",
	"onboarding":       "onboarding.update",
	"panel-activate":   "panel.activate",
	"panel-install":    "panel.install",
	"panel-reinstall":  "panel.reinstall",
	"panel-rollback":   "panel.rollback",
	"panel-uninstall":  "panel.uninstall",
	"panel-update":     "panel.update",
	"preferences-tui":  "preferences.update",
	"restart":          "core.restart",
	"rule-provider":    "rule_provider.refresh",
	"select":           "proxy.select",
	"sub-add":          "subscription.add",
	"sub-refresh":      "subscription.refresh",
	"sub-remove":       "subscription.remove",
	"sub-set":          "subscription.set",
	"sub-enabled":      "subscription.enabled",
	"sub-use":          "subscription.use",
	"sysproxy-disable": "system_proxy.disable",
	"sysproxy-enable":  "system_proxy.enable",
	"tun-disable":      "tun.disable",
	"tun-enable":       "tun.enable",
}
