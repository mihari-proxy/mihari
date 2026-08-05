//go:build windows

package transport

import (
	"context"
	"net"
	"os"
	"path/filepath"

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
	// Machine-wide path so LocalSystem service and interactive clients share one token.
	// (UserConfigDir under LocalSystem is the systemprofile hive and is not shared.)
	if dir := os.Getenv("ProgramData"); dir != "" {
		return filepath.Join(dir, "mihari", "control.token")
	}
	return filepath.Join(`C:\ProgramData`, "mihari", "control.token")
}
