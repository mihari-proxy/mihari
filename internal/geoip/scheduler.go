package geoip

import (
	"context"
	"time"
)

const (
	// DefaultRefreshAge is the maximum age of either active database.
	DefaultRefreshAge = 30 * 24 * time.Hour
	// DefaultCheckInterval controls how often database age is evaluated.
	DefaultCheckInterval = 24 * time.Hour
)

// Scheduler periodically refreshes a missing or stale database pair.
type Scheduler struct {
	NeedsUpdate func(time.Time, time.Duration) bool
	Refresh     func(context.Context) error
	Now         func() time.Time
	After       func(time.Duration) <-chan time.Time
	MaxAge      time.Duration
	CheckEvery  time.Duration
}

// Run checks immediately and then at the configured interval until cancellation.
func (s Scheduler) Run(ctx context.Context) error {
	if s.NeedsUpdate == nil || s.Refresh == nil {
		<-ctx.Done()
		return nil
	}
	now := s.Now
	if now == nil {
		now = time.Now
	}
	after := s.After
	if after == nil {
		after = time.After
	}
	maxAge := s.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultRefreshAge
	}
	checkEvery := s.CheckEvery
	if checkEvery <= 0 {
		checkEvery = DefaultCheckInterval
	}
	for {
		if s.NeedsUpdate(now().UTC(), maxAge) {
			_ = s.Refresh(ctx)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-after(checkEvery):
		}
	}
}
