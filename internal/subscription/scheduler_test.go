package subscription

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestDueRespectsEnabledAutoAndPerProfileInterval(t *testing.T) {
	now := time.Unix(10_000, 0).UTC()
	catalog := Defaults()
	catalog.Profiles = []Profile{
		{ID: "00000000000000000000000000000001", Enabled: true, AutoRefresh: true, UpdatedAt: now.Add(-13 * time.Hour)},
		{ID: "00000000000000000000000000000002", Enabled: true, AutoRefresh: true, Interval: "30m", UpdatedAt: now.Add(-time.Hour)},
		{ID: "00000000000000000000000000000003", Enabled: false, AutoRefresh: true},
		{ID: "00000000000000000000000000000004", Enabled: true, AutoRefresh: false},
		{ID: "00000000000000000000000000000005", Enabled: true, AutoRefresh: true, UpdatedAt: now.Add(-time.Hour)},
	}
	ids := Due(catalog, now)
	if len(ids) != 2 || ids[0] != catalog.Profiles[0].ID || ids[1] != catalog.Profiles[1].ID {
		t.Fatalf("due=%v", ids)
	}
}

func TestRetryDelayIsExponentialAndBounded(t *testing.T) {
	for failures, want := range map[int]time.Duration{1: time.Minute, 2: 2 * time.Minute, 6: 30 * time.Minute, 20: 30 * time.Minute} {
		if got := RetryDelay(failures, time.Minute, 30*time.Minute); got != want {
			t.Fatalf("failures=%d got=%v want=%v", failures, got, want)
		}
	}
}

func TestSchedulerCallsRefreshAndStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int64
	catalog := Defaults()
	catalog.Profiles = []Profile{{ID: "00000000000000000000000000000001", Enabled: true, AutoRefresh: true}}
	scheduler := NewScheduler(SchedulerOptions{
		Snapshot: func() Catalog { return catalog },
		Refresh: func(context.Context, string) error {
			calls.Add(1)
			cancel()
			return nil
		},
		IdlePoll: time.Millisecond,
	})
	err := scheduler.Run(ctx)
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}
