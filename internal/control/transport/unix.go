//go:build !windows

package transport

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func Listen(endpoint string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(endpoint), 0o700); err != nil {
		return nil, err
	}
	if err := removeStaleSocket(endpoint); err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(endpoint, 0o600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

func removeStaleSocket(endpoint string) error {
	info, err := os.Lstat(endpoint)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("control endpoint exists and is not a socket: %s", endpoint)
	}
	return os.Remove(endpoint)
}

func DialContext(ctx context.Context, endpoint string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
}

func DefaultEndpoint() string {
	if value := os.Getenv("MIHARI_CONTROL_ENDPOINT"); value != "" {
		return value
	}
	// Linux: prefer XDG_RUNTIME_DIR (often cleared on logout).
	// macOS and Linux without XDG_RUNTIME_DIR: socket under data root.
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, "mihari", "control.sock")
	}
	return filepath.Join(platform.DefaultDataRoot(), "control.sock")
}

func DefaultCredentialPath() string {
	if value := os.Getenv("MIHARI_CONTROL_CREDENTIAL"); value != "" {
		return value
	}
	return platform.DefaultPaths().ControlToken
}
