package ui

// SnapshotState distinguishes an unobserved resource from a confirmed empty one.
type SnapshotState struct {
	Loaded  bool
	Pending bool
	Failure string
}

// Placeholder uses an empty-state label only after a successful observation.
func (s SnapshotState) Placeholder(empty string) string {
	if s.Failure != "" {
		return "Unavailable: " + s.Failure
	}
	if !s.Loaded {
		return "⠋ Loading…"
	}
	return empty
}

// RefreshSnapshotsMsg routes ordinary page reads through the shared scheduler.
type RefreshSnapshotsMsg struct {
	Page          PageID
	AfterMutation bool
}

// ObservedMetric avoids displaying an unobserved measurement as zero.
func ObservedMetric(value string, unknown bool) string {
	if unknown {
		return "—"
	}
	return value
}
