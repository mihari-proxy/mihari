package ui

// VisibleWindow returns the [start, end) index window of count items that
// fits in height rows after chrome fixed rows, keeping focused inside.
//
// Semantics (anchored-bottom scrolling, same as the Logs page):
//   - chrome is the page chrome outside the table rows (controls, header,
//     borders); rows := max(1, height-chrome)
//   - count <= rows returns the whole list (0, count)
//   - following pins the window to the bottom (count-rows, count)
//   - otherwise focused is clamped into the window; a negative focused is
//     treated as 0
func VisibleWindow(count, height, chrome int, following bool, focused int) (start, end int) {
	rows := max(1, height-chrome)
	if count <= rows {
		return 0, count
	}
	if following {
		return count - rows, count
	}
	start = min(max(0, focused-rows+1), count-rows)
	return start, start + rows
}

// EnsureLineVisible keeps [focusStart, focusEnd) inside a window of viewH
// lines and returns the new scrollY. n is len(lines).
//
// This is a line-based viewport (terminal rows + half-open focus block).
// Do not confuse it with VisibleWindow, which is an item-index,
// anchored-bottom table window used by logs/rules/connections.
//
// Semantics (match proxies ensureFocusVisible):
//   - viewH <= 0: return scrollY unchanged
//   - n == 0: return 0
//   - focusStart < 0 || focusEnd <= focusStart: clamp scrollY into [0, max(0,n-viewH)]
//   - focusEnd-focusStart >= viewH: pin scrollY = focusStart, then clamp
//   - else keep the block inside [scrollY, scrollY+viewH), then clamp
func EnsureLineVisible(scrollY, viewH, n, focusStart, focusEnd int) int {
	if viewH <= 0 {
		return scrollY
	}
	if n == 0 {
		return 0
	}
	maxScroll := max(0, n-viewH)
	clamp := func(y int) int { return min(max(0, y), maxScroll) }
	if focusStart < 0 || focusEnd <= focusStart {
		return clamp(scrollY)
	}
	if focusEnd-focusStart >= viewH {
		scrollY = focusStart
	} else {
		if focusStart < scrollY {
			scrollY = focusStart
		}
		if focusEnd > scrollY+viewH {
			scrollY = focusEnd - viewH
		}
	}
	return clamp(scrollY)
}

// SliceLines returns the visible window of lines.
// height <= 0 means "not sized yet" and returns lines unchanged (same
// backing slice is allowed). Never panics on empty input or out-of-range
// scrollY. Do not pass height 0 to mean "empty window".
func SliceLines(lines []string, scrollY, height int) []string {
	if height <= 0 {
		return lines
	}
	n := len(lines)
	start := min(max(0, scrollY), max(0, n-height))
	return lines[start : start+min(height, n-start)]
}
