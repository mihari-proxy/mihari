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
