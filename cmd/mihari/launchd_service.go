package main

import (
	"context"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func runLaunchdServiceWithGroup(ctx context.Context, pid int, processGroup func(int) (int, error), run func(context.Context, bool) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	invalid := protocol.APIError{Code: protocol.CodeInvalidState, Message: "launchd service must lead its process group"}
	if pid <= 1 {
		return invalid
	}
	group, err := processGroup(pid)
	if err != nil || group != pid {
		return invalid
	}
	return run(ctx, true)
}
