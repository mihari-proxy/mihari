//go:build windows

package transport

import (
	"context"
	"net"
	"os"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/Microsoft/go-winio"
)

func Listen(endpoint string) (net.Listener, error) {
	// LocalSystem service creates the pipe: allow SYSTEM, Administrators, and
	// interactive users so a non-admin desktop TUI/CLI can still connect.
	config := &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)(A;;GRGW;;;AU)",
		MessageMode:        false,
		InputBufferSize:    64 * 1024,
		OutputBufferSize:   64 * 1024,
	}
	return winio.ListenPipe(endpoint, config)
}

func DialContext(ctx context.Context, endpoint string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, endpoint)
}

func DefaultEndpoint() string {
	if value := os.Getenv("MIHARI_CONTROL_ENDPOINT"); value != "" {
		return value
	}
	return `\\.\pipe\mihari-control`
}

func DefaultCredentialPath() string {
	if value := os.Getenv("MIHARI_CONTROL_CREDENTIAL"); value != "" {
		return value
	}
	// Token lives under the shared data root ($HOME/.mihari/control.token).
	// Service installs pin MIHARI_DATA so LocalSystem uses the same file as the desktop user.
	return platform.DefaultPaths().ControlToken
}
