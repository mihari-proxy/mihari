package subscription

import (
	"context"
	"hash/fnv"
	"time"
)

type SchedulerOptions struct {
	Snapshot func() Catalog
	Refresh  func(context.Context, string) error
	Now      func() time.Time
	After    func(time.Duration) <-chan time.Time
	Jitter   func(string, time.Duration) time.Duration
	IdlePoll time.Duration
	MinRetry time.Duration
	MaxRetry time.Duration
}

type Scheduler struct {
	options SchedulerOptions
}

func NewScheduler(options SchedulerOptions) *Scheduler {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.After == nil {
		options.After = time.After
	}
	if options.Jitter == nil {
		options.Jitter = stableJitter
	}
	if options.IdlePoll <= 0 {
		options.IdlePoll = time.Minute
	}
	if options.MinRetry <= 0 {
		options.MinRetry = time.Minute
	}
	if options.MaxRetry <= 0 {
		options.MaxRetry = 30 * time.Minute
	}
	return &Scheduler{options: options}
}

func Due(catalog Catalog, now time.Time) []string {
	result := make([]string, 0)
	for _, profile := range catalog.Profiles {
		if !profile.Enabled || !profile.AutoRefresh {
			continue
		}
		if profile.UpdatedAt.IsZero() || !now.Before(profile.UpdatedAt.Add(catalog.EffectiveInterval(profile))) {
			result = append(result, profile.ID)
		}
	}
	return result
}

func RetryDelay(failures int, minimum, maximum time.Duration) time.Duration {
	if failures < 1 {
		failures = 1
	}
	delay := minimum
	for index := 1; index < failures && delay < maximum; index++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func (s *Scheduler) Run(ctx context.Context) error {
	if s.options.Snapshot == nil || s.options.Refresh == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	failures := make(map[string]int)
	nextAllowed := make(map[string]time.Time)
	for {
		now := s.options.Now().UTC()
		catalog := s.options.Snapshot()
		dueIDs, wait := s.due(catalog, now, nextAllowed)
		if len(dueIDs) == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-s.options.After(wait):
			}
			continue
		}
		for _, id := range dueIDs {
			if err := s.options.Refresh(ctx, id); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				failures[id]++
				nextAllowed[id] = now.Add(RetryDelay(failures[id], s.options.MinRetry, s.options.MaxRetry) + s.options.Jitter(id+"retry", s.options.MinRetry))
				continue
			}
			delete(failures, id)
			if index := catalog.Index(id); index >= 0 {
				interval := catalog.EffectiveInterval(catalog.Profiles[index])
				nextAllowed[id] = now.Add(interval + s.options.Jitter(id, interval))
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func (s *Scheduler) due(catalog Catalog, now time.Time, nextAllowed map[string]time.Time) ([]string, time.Duration) {
	result := make([]string, 0)
	wait := s.options.IdlePoll
	for _, profile := range catalog.Profiles {
		if !profile.Enabled || !profile.AutoRefresh {
			continue
		}
		dueAt := now
		if !profile.UpdatedAt.IsZero() {
			interval := catalog.EffectiveInterval(profile)
			dueAt = profile.UpdatedAt.Add(interval + s.options.Jitter(profile.ID, interval))
		}
		if allowed := nextAllowed[profile.ID]; allowed.After(dueAt) {
			dueAt = allowed
		}
		if !now.Before(dueAt) {
			result = append(result, profile.ID)
			continue
		}
		remaining := dueAt.Sub(now)
		if remaining < wait {
			wait = remaining
		}
	}
	if wait <= 0 {
		wait = time.Millisecond
	}
	return result, wait
}

func stableJitter(id string, interval time.Duration) time.Duration {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(id))
	// Deterministic +/-5% avoids synchronized refreshes while keeping tests and
	// restarts predictable for a stable subscription ID.
	parts := int64(hash.Sum32()%101) - 50
	return time.Duration(int64(interval) * parts / 1000)
}
