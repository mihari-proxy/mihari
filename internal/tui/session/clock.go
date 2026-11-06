package session

import "time"

type snapshotClock interface {
	Now() time.Time
	NewTimer() snapshotTimer
}
type snapshotTimer interface {
	Channel() <-chan time.Time
	Reset(time.Duration)
	Stop()
}
type wallSnapshotClock struct{}

func (wallSnapshotClock) Now() time.Time { return time.Now() }
func (wallSnapshotClock) NewTimer() snapshotTimer {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	return wallSnapshotTimer{timer}
}

type wallSnapshotTimer struct{ timer *time.Timer }

func (t wallSnapshotTimer) Channel() <-chan time.Time { return t.timer.C }
func (t wallSnapshotTimer) Reset(delay time.Duration) { t.timer.Reset(delay) }
func (t wallSnapshotTimer) Stop() {
	if !t.timer.Stop() {
		select {
		case <-t.timer.C:
		default:
		}
	}
}
