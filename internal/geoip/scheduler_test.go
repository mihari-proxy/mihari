package geoip

import (
	"context"
	"testing"
	"time"
)

func TestSchedulerRefreshesOnlyWhenDatabasePairIsStale(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	refreshes := 0
	scheduler := Scheduler{
		NeedsUpdate: func(time.Time, time.Duration) bool { return true },
		Refresh: func(context.Context) error {
			refreshes++
			cancel()
			return nil
		},
		Now: func() time.Time { return time.Unix(1, 0) },
	}
	if err := scheduler.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 {
		t.Fatalf("refreshes=%d", refreshes)
	}
}
