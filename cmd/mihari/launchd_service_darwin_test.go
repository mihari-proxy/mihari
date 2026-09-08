package main

import (
	"context"
	"testing"
)

func TestLaunchdService_DarwinProvidesCapability(t *testing.T) {
	if launchdServiceDaemon(func(context.Context, bool) error { return nil }) == nil {
		t.Fatal("Darwin installed service capability is unavailable")
	}
}
