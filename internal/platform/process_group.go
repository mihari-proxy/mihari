package platform

import (
	"context"
	"errors"
	"fmt"
)

var errProcessGroupUnknown = errors.New("launchd process group is unknown")

func darwinGroupHasPeers(ctx context.Context, pid, group int, list func(int, *[2]int32) (int, error)) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if pid <= 1 || int64(pid) > 1<<31-1 || group != pid || list == nil {
		return false, errProcessGroupUnknown
	}
	var pids [2]int32
	n, err := list(group, &pids)
	if canceled := ctx.Err(); canceled != nil {
		return false, canceled
	}
	if err != nil {
		return false, fmt.Errorf("inspect launchd process group: %w", err)
	}
	if n != 4 && n != 8 {
		return false, errProcessGroupUnknown
	}
	if n == 4 {
		if pids[0] != int32(pid) {
			return false, errProcessGroupUnknown
		}
		return false, nil
	}
	if pids[0] <= 0 || pids[1] <= 0 || pids[0] == pids[1] {
		return false, errProcessGroupUnknown
	}
	// A full sample may omit the owner when more than two processes exist.
	// It is sufficient evidence of peers; no identities are adopted or signaled.
	return true, nil
}
