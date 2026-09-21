//go:build windows

package server

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
)

func applicationUpdateIdentity(ctx context.Context, id string) (protocol.ApplicationUpdatePrepared, error) {
	process, err := platform.OpenWindowsProcessIdentity(ctx, uint32(os.Getpid()))
	if err != nil {
		return protocol.ApplicationUpdatePrepared{}, err
	}
	identity := process.Identity()
	return protocol.ApplicationUpdatePrepared{Schema: "mihari/v1", OperationID: id, PID: identity.PID, CreationFiletime: identity.CreationFiletime, ImagePath: identity.ImagePath, SID: identity.SID}, process.Close()
}
