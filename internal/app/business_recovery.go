package app

import (
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/platform"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"strings"
)

// NewBusinessRecovery exposes only authenticated reset operations while startup is degraded.
// It opens no business stores and never starts mihomo or a network gateway.
func NewBusinessRecovery(paths platform.Paths, store *state.Store, reporter diagnostics.Reporter) *runtimeapi.Manager {
	settings, err := config.LoadForProxyAuthReset(paths.Settings)
	allowed := err == nil
	if !allowed {
		settings = config.Defaults()
		settings.ControllerSecret = strings.Repeat("0", 64)
	}
	return runtimeapi.New(runtimeapi.Options{RecoveryOnly: true, AuthResetAllowed: allowed, Store: store, Settings: settings, SettingsPath: paths.Settings, BusinessReset: BusinessReset{Paths: paths, Reporter: reporter}, DiagnosticReporter: reporter})
}
