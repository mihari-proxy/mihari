package main

import (
	"context"
	"os"
	"syscall"
)

func launchdServiceDaemon(run func(context.Context, bool) error) func(context.Context) error {
	return func(ctx context.Context) error {
		return runLaunchdServiceWithGroup(ctx, os.Getpid(), syscall.Getpgid, run)
	}
}
