// Package sysproxy provides OS system HTTP(S) proxy control and ownership helpers.
// Platform backends implement unexported get/enable/disable; Task 1 ships stubs.
package sysproxy

import (
	"fmt"
	"net"
	"strings"
)

// State is the observed system proxy configuration.
type State struct {
	Enabled bool
	Server  string
}

// Backend is an injectable system-proxy driver for tests and runtime wiring.
type Backend interface {
	Get() (State, error)
	Enable(host string, port int) error
	Disable() error
}

// Get returns the current system proxy state from the platform backend.
func Get() (State, error) {
	return get()
}

// Enable turns on the system proxy for host:port.
// Blank host defaults to 127.0.0.1. Port must be in 1..65535.
func Enable(host string, port int) error {
	host, port, err := normalizeEnableArgs(host, port)
	if err != nil {
		return err
	}
	return enable(host, port)
}

// Disable turns off the system proxy via the platform backend.
func Disable() error {
	return disable()
}

// NormalizeServer formats host and port as a proxy server string (net.JoinHostPort).
func NormalizeServer(host string, port int) string {
	return net.JoinHostPort(host, fmt.Sprintf("%d", port))
}

func normalizeEnableArgs(host string, port int) (string, int, error) {
	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("sysproxy: invalid port %d", port)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		host = "127.0.0.1"
	}
	return host, port, nil
}
