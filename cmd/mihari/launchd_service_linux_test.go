package main

import (
	"context"
	"testing"
)

func TestLaunchdService_LinuxDoesNotProvideCapability(t *testing.T) {
	if launchdServiceDaemon(func(context.Context, bool) error { return nil }) != nil {
		t.Fatal("Linux exposed launchd startup capability")
	}
}
