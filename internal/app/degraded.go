package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
)

// NewDegradedStore returns an in-memory store with health=degraded and a
// sanitized last_error derived from a daemon assembly failure.
func NewDegradedStore(version string, err error) *state.Store {
	return state.NewStore(state.Snapshot{
		Version:   version,
		StartedAt: time.Now().UTC(),
		Health:    state.HealthDegraded,
		LastError: FormatStartupError(err),
	})
}

// FormatStartupError returns a control-plane last_error that never includes
// secrets, tokens, or raw unknown error text.
func FormatStartupError(err error) string {
	var apiError protocol.APIError
	if !errors.As(err, &apiError) {
		return "daemon startup failed"
	}
	if apiError.Message == "managed port is unavailable" {
		if message, ok := formatPortUnavailableError(apiError.Details); ok {
			return message
		}
	}
	return apiError.Message
}

func formatPortUnavailableError(details map[string]any) (string, bool) {
	setting, ok := detailString(details, "setting")
	if !ok || setting == "" {
		return "", false
	}
	address, ok := detailString(details, "address")
	if !ok || address == "" {
		return "", false
	}
	process, hasProcess := detailString(details, "process")
	pid, hasPID := detailInt(details, "pid")
	if hasProcess && hasPID && process != "" && pid > 0 {
		return fmt.Sprintf("managed port %s %s is held by %s (pid %d)", setting, address, process, pid), true
	}
	return fmt.Sprintf("managed port %s %s is unavailable", setting, address), true
}

func detailString(details map[string]any, key string) (string, bool) {
	value, ok := details[key].(string)
	return value, ok
}

func detailInt(details map[string]any, key string) (int, bool) {
	value, ok := details[key].(int)
	return value, ok
}
