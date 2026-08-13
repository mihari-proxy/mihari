package ui

import "fmt"

// FormatPositionIndicator formats the list position indicator "X/Total".
// pos is the 1-based focused row index within the filtered list; total is the
// filtered visible count. When the focus is not on a data row, pass focused=false
// to render "—/Total". Empty lists always render "0/0". When focused is false,
// pos is ignored.
func FormatPositionIndicator(focused bool, pos, total int) string {
	if total <= 0 {
		return "0/0"
	}
	if !focused {
		return fmt.Sprintf("—/%d", total)
	}
	return fmt.Sprintf("%d/%d", pos, total)
}
