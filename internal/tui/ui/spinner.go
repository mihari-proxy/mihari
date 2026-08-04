package ui

import "time"

// brailleSpinnerFrames is the classic Braille spinner sequence (§4.8).
var brailleSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const defaultSpinnerPeriod = 100 * time.Millisecond

// SpinnerFrame returns the Braille spinner glyph for now given period.
// If period <= 0, period is treated as 100ms.
func SpinnerFrame(now time.Time, period time.Duration) string {
	if period <= 0 {
		period = defaultSpinnerPeriod
	}
	// Use UnixNano so epoch-aligned tests and wall-clock now both work.
	// Division by period yields a non-negative frame index that wraps.
	elapsed := now.UnixNano()
	if elapsed < 0 {
		// Negative times still map stably into the frame ring.
		elapsed = -elapsed
	}
	idx := int((elapsed / int64(period)) % int64(len(brailleSpinnerFrames)))
	return brailleSpinnerFrames[idx]
}

// SpinnerLabel returns "frame label" using the default ~100ms period.
func SpinnerLabel(now time.Time, label string) string {
	return SpinnerFrame(now, defaultSpinnerPeriod) + " " + label
}
