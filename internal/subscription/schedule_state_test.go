package subscription

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSubscriptionSchedule_ResetDefersExpiredProfile(t *testing.T) {
	now := time.Unix(100000, 0).UTC()
	c := Defaults()
	c.Profiles = []Profile{{ID: "primary", Enabled: true, AutoRefresh: true, UpdatedAt: now.Add(-24 * time.Hour), ScheduleFrom: now, Interval: "1h"}}
	if len(Due(c, now)) != 0 {
		t.Fatal("old cache age triggered a newly reset schedule")
	}
	s := NewScheduler(SchedulerOptions{Jitter: func(string, time.Duration) time.Duration { return 0 }})
	ids, _ := s.due(c, now, nil)
	if len(ids) != 0 {
		t.Fatal("scheduler ignored reset")
	}
	if len(Due(c, now.Add(time.Hour))) != 1 {
		t.Fatal("reset schedule never became due")
	}
}

func TestSubscriptionSchedule_ResetReplacesOldWait(t *testing.T) {
	now := time.Unix(100000, 0).UTC()
	c := Defaults()
	c.Profiles = []Profile{{ID: "primary", Enabled: true, AutoRefresh: true, Interval: "12h"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	waiting := 0
	s := NewScheduler(SchedulerOptions{
		Now: func() time.Time { return now }, Snapshot: func() Catalog { return c.Clone() },
		Jitter: func(string, time.Duration) time.Duration { return 0 }, IdlePoll: 24 * time.Hour,
		Refresh: func(context.Context, string) error {
			calls++
			c.Profiles[0].UpdatedAt = now
			if calls == 2 {
				cancel()
			}
			return nil
		},
		After: func(delay time.Duration) <-chan time.Time {
			waiting++
			if waiting == 1 {
				c.Profiles[0].Interval = "1h"
				c.Profiles[0].ScheduleFrom = now
				now = now.Add(time.Hour)
			} else {
				t.Errorf("new interval was delayed by old wait: %s", delay)
				cancel()
			}
			ready := make(chan time.Time, 1)
			ready <- now
			return ready
		},
	})
	if err := s.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("refresh calls=%d, want 2", calls)
	}
}

func TestSubscriptionSchedule_RechecksQueuedProfiles(t *testing.T) {
	now := time.Unix(100000, 0).UTC()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := Defaults()
	c.Profiles = []Profile{{ID: "a", Enabled: true, AutoRefresh: true}, {ID: "b", Enabled: true, AutoRefresh: true}}
	calls := 0
	s := NewScheduler(SchedulerOptions{Now: func() time.Time { return now }, Snapshot: func() Catalog { return c.Clone() }, Jitter: func(string, time.Duration) time.Duration { return 0 },
		Refresh: func(_ context.Context, id string) error {
			calls++
			if id == "b" {
				t.Error("queued profile was fetched after its schedule changed")
			}
			c.Profiles[0].UpdatedAt = now
			c.Profiles[1].ScheduleFrom = now
			return nil
		},
		After: func(time.Duration) <-chan time.Time { cancel(); return make(chan time.Time) }})
	if err := s.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("refresh calls=%d", calls)
	}
}

func TestSubscriptionSchedule_EditWakesBeforeOldTimer(t *testing.T) {
	now := time.Unix(100000, 0).UTC()
	c := Defaults()
	c.Profiles = []Profile{{ID: "a", Enabled: true, AutoRefresh: true, Interval: "12h", UpdatedAt: now}}
	changes := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls, waits := 0, 0
	s := NewScheduler(SchedulerOptions{Changes: changes, Now: func() time.Time { return now }, Snapshot: func() Catalog { return c.Clone() }, Jitter: func(string, time.Duration) time.Duration { return 0 },
		Refresh: func(context.Context, string) error { calls++; cancel(); return nil },
		After: func(time.Duration) <-chan time.Time {
			waits++
			if waits == 1 {
				c.Profiles[0].Interval = "1s"
				c.Profiles[0].ScheduleFrom = now
				changes <- struct{}{}
			} else {
				now = now.Add(time.Second)
			}
			timer := make(chan time.Time, 1)
			if waits > 1 {
				timer <- now
			}
			return timer
		}})
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("catalog edit did not wake scheduler before the old timer")
	}
	if calls != 1 || waits != 2 {
		t.Fatalf("refreshes=%d waits=%d", calls, waits)
	}
}
