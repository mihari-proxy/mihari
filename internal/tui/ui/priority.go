package ui

import (
	"sort"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

// PrioritySegment is one segment of a priority-rendered bar (status bar).
// Render is called at most once per frame per segment.
type PrioritySegment struct {
	// Priority is the drop order when width runs out: larger survives longer.
	// 0 means never dropped (always kept).
	Priority int
	Render   func() string
}

// PriorityBar lays out segments by the continuous-prefix drop rule: render
// each segment once, keep the highest-priority prefix that fits within width,
// then join the kept segments in their original (positional) order with sep.
// If nothing at all fits and no always-kept segment exists, the single
// highest-priority segment is hard-truncated to width (truncateStyled fallback).
func PriorityBar(width int, segments []PrioritySegment, sep string) string {
	if width <= 0 || len(segments) == 0 {
		return ""
	}
	type measured struct {
		index int
		text  string
		w     int
	}
	var always, droppable []measured
	for index, seg := range segments {
		text := seg.Render()
		m := measured{index: index, text: text, w: lipgloss.Width(text)}
		if seg.Priority <= 0 {
			always = append(always, m)
		} else {
			droppable = append(droppable, m)
		}
	}
	sort.SliceStable(droppable, func(i, j int) bool {
		return segments[droppable[i].index].Priority > segments[droppable[j].index].Priority
	})

	// sepWidth is the visible width (len() counts UTF-8 bytes for ·).
	sepWidth := lipgloss.Width(sep)
	selected := append([]measured{}, always...)
	used := 0
	for _, m := range selected {
		used += m.w
	}
	if len(selected) > 1 {
		used += sepWidth * (len(selected) - 1)
	}
	for _, m := range droppable {
		add := m.w
		if len(selected) > 0 {
			add += sepWidth
		}
		if used+add > width {
			break
		}
		selected = append(selected, m)
		used += add
	}
	if len(selected) == 0 {
		return truncateStyled(droppable[0].text, width)
	}
	// Keep segments are joined in original positional order.
	sort.SliceStable(selected, func(i, j int) bool {
		return selected[i].index < selected[j].index
	})
	texts := make([]string, 0, len(selected))
	for _, m := range selected {
		texts = append(texts, m.text)
	}
	return strings.Join(texts, sep)
}

// FitPriorityColumns keeps the highest-priority continuous prefix of cols that
// fits within total (MinWidth as the measured width, gap applied between
// columns), then distributes width with FitColumnWidths over the kept columns.
// Priority=0 columns are always kept and their MinWidth is pre-deducted.
// Returned columns are in their original positional order, paired with widths.
// Fallback: if no droppable column fits (and no always-kept column exists),
// the single highest-priority column is returned hard-shrunk to total.
func FitPriorityColumns(cols []TableColumn, total, gap int) ([]TableColumn, []int) {
	if len(cols) == 0 {
		return nil, nil
	}
	if gap < 0 {
		gap = 0
	}
	var always, droppable []TableColumn
	for _, col := range cols {
		if col.Priority <= 0 {
			always = append(always, col)
		} else {
			droppable = append(droppable, col)
		}
	}
	// Droppable columns sorted by priority descending, stable (equal priority
	// keeps positional order).
	sort.SliceStable(droppable, func(i, j int) bool {
		return droppable[i].Priority > droppable[j].Priority
	})

	selected := append([]TableColumn{}, always...)
	used := 0
	for _, col := range selected {
		used += max(1, col.MinWidth)
	}
	if len(selected) > 1 {
		used += gap * (len(selected) - 1)
	}
	for _, col := range droppable {
		add := max(1, col.MinWidth)
		if len(selected) > 0 {
			add += gap
		}
		if used+add > total {
			break
		}
		selected = append(selected, col)
		used += add
	}
	if len(selected) == 0 {
		return []TableColumn{droppable[0]}, []int{max(1, total)}
	}
	widths := FitColumnWidths(selected, total, gap)
	// Reorder by original positional order (droppable was sorted by priority).
	ordered := make([]TableColumn, 0, len(selected))
	orderedWidths := make([]int, 0, len(selected))
	for _, col := range cols {
		for index, sel := range selected {
			if sel.ID != col.ID {
				continue
			}
			ordered = append(ordered, sel)
			orderedWidths = append(orderedWidths, widths[index])
			break
		}
	}
	return ordered, orderedWidths
}
