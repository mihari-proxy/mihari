//go:build linux || darwin

package app

import (
	"context"

	"github.com/mihari-proxy/mihari/internal/service"
)

// ManagedDaemonPID identifies the installed service before an endpoint restart.
func (i *UnixInstaller) ManagedDaemonPID(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	definition, err := i.adapter(nil).Probe(ctx)
	if err != nil {
		return 0, ClassifyUnixLocalError(err)
	}
	if definition.Status != service.StatusRunning || !definition.Running {
		return 0, nil
	}
	return definition.Process.PID, nil
}
