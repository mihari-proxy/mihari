package ui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func colsWithPriority() []TableColumn {
	// Modeled on a subscription table: name always kept, priorities 6..1.
	return []TableColumn{
		{ID: "name", Title: "Name", MinWidth: 10, Flex: 3, Priority: 0},
		{ID: "active", Title: "Active", MinWidth: 6, Flex: 0, Priority: 6},
		{ID: "state", Title: "State", MinWidth: 8, Flex: 0, Priority: 5},
		{ID: "load", Title: "Load", MinWidth: 9, Flex: 0, Priority: 4},
		{ID: "traffic", Title: "Traffic", MinWidth: 11, Flex: 1, Priority: 3},
		{ID: "last", Title: "Last update", MinWidth: 11, Flex: 0, Priority: 2},
		{ID: "next", Title: "Next update", MinWidth: 11, Flex: 0, Priority: 1},
	}
}

func ids(cols []TableColumn) string {
	parts := make([]string, 0, len(cols))
	for _, col := range cols {
		parts = append(parts, col.ID)
	}
	return strings.Join(parts, ",")
}

func TestFitPriorityColumns_ByBudget(t *testing.T) {
	cols := colsWithPriority()
	// Cumulative MinWidth: 1:10, 2:18, 3:28, 4:39, 5:52, 6:65, 7:78 (gap 2).
	cases := []struct {
		name  string
		total int
		gap   int
		want  string // comma-joined kept column IDs in positional order
	}{
		{"fits-all", 84, 2, "name,active,state,load,traffic,last,next"},
		{"exact-fit", 78, 2, "name,active,state,load,traffic,last,next"},
		{"drops-next", 76, 2, "name,active,state,load,traffic,last"},
		{"drops-last", 64, 2, "name,active,state,load,traffic"},
		{"drops-traffic", 51, 2, "name,active,state,load"},
		{"drops-load", 38, 2, "name,active,state"},
		{"drops-state", 18, 2, "name,active"},
		{"only-always", 16, 2, "name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, widths := FitPriorityColumns(cols, tc.total, tc.gap)
			if got := ids(kept); got != tc.want {
				t.Fatalf("total=%d kept=%q want %q", tc.total, got, tc.want)
			}
			if len(kept) != len(widths) {
				t.Fatalf("widths len=%d kept len=%d", len(widths), len(kept))
			}
			// Widths must respect MinWidth (selection guarantees fit).
			for index, col := range kept {
				if widths[index] < max(1, col.MinWidth) {
					t.Fatalf("col %s width %d < min %d", col.ID, widths[index], col.MinWidth)
				}
			}
		})
	}
}

func TestFitPriorityColumns_EdgeCases(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		kept, widths := FitPriorityColumns(nil, 50, 2)
		if kept != nil || widths != nil {
			t.Fatalf("kept=%v widths=%v", kept, widths)
		}
	})
	t.Run("all-fixed", func(t *testing.T) {
		cols := []TableColumn{
			{ID: "a", MinWidth: 10, Flex: 0, Priority: 2},
			{ID: "b", MinWidth: 10, Flex: 0, Priority: 1},
		}
		// 10 + gap 2 + 10 = 22 exactly: both kept at MinWidth.
		kept, widths := FitPriorityColumns(cols, 22, 2)
		if ids(kept) != "a,b" {
			t.Fatalf("kept=%q", ids(kept))
		}
		if widths[0] != 10 || widths[1] != 10 {
			t.Fatalf("widths=%v", widths)
		}
		// Budget 21: b (priority 1) dropped, a kept at min.
		kept, widths = FitPriorityColumns(cols, 21, 2)
		if ids(kept) != "a" || widths[0] != 10 {
			t.Fatalf("kept=%q widths=%v", ids(kept), widths)
		}
	})
	t.Run("over-budget-fallback", func(t *testing.T) {
		// No always-kept column and even the top one does not fit: hard shrink.
		cols := []TableColumn{
			{ID: "a", MinWidth: 30, Flex: 0, Priority: 2},
			{ID: "b", MinWidth: 20, Flex: 0, Priority: 1},
		}
		kept, widths := FitPriorityColumns(cols, 10, 2)
		if ids(kept) != "a" {
			t.Fatalf("kept=%q", ids(kept))
		}
		if widths[0] != 10 {
			t.Fatalf("widths=%v", widths)
		}
	})
	t.Run("equal-priority-keeps-order", func(t *testing.T) {
		cols := []TableColumn{
			{ID: "a", MinWidth: 4, Flex: 0, Priority: 5},
			{ID: "b", MinWidth: 4, Flex: 0, Priority: 5},
			{ID: "c", MinWidth: 4, Flex: 0, Priority: 5},
		}
		// 3 cols need 16; budget 12 keeps the positional prefix a,b.
		kept, _ := FitPriorityColumns(cols, 12, 2)
		if ids(kept) != "a,b" {
			t.Fatalf("kept=%q", ids(kept))
		}
		// Budget 16 keeps all in positional order.
		kept, _ = FitPriorityColumns(cols, 16, 2)
		if ids(kept) != "a,b,c" {
			t.Fatalf("kept=%q", ids(kept))
		}
	})
	t.Run("always-pre-deducted", func(t *testing.T) {
		// Always-kept column takes 10 + gaps; droppable needs 4+2 more.
		cols := []TableColumn{
			{ID: "keep", MinWidth: 10, Flex: 0, Priority: 0},
			{ID: "drop", MinWidth: 4, Flex: 0, Priority: 1},
		}
		kept, _ := FitPriorityColumns(cols, 14, 2)
		if ids(kept) != "keep" {
			t.Fatalf("kept=%q want keep only", ids(kept))
		}
	})
	t.Run("positional-order-preserved", func(t *testing.T) {
		// Droppable priority order differs from positional order.
		cols := []TableColumn{
			{ID: "low", MinWidth: 4, Flex: 0, Priority: 1},
			{ID: "high", MinWidth: 4, Flex: 0, Priority: 9},
			{ID: "mid", MinWidth: 4, Flex: 0, Priority: 5},
		}
		kept, _ := FitPriorityColumns(cols, 20, 2)
		if ids(kept) != "low,high,mid" {
			t.Fatalf("kept=%q", ids(kept))
		}
		// Tight budget drops low first but keeps positional order of the rest.
		kept, _ = FitPriorityColumns(cols, 12, 2)
		if ids(kept) != "high,mid" {
			t.Fatalf("kept=%q", ids(kept))
		}
	})
}

func TestPriorityBar_ByBudget(t *testing.T) {
	segments := []PrioritySegment{
		{Priority: 6, Render: func() string { return "Mihari" }},
		{Priority: 5, Render: func() string { return "● running" }},
		{Priority: 1, Render: func() string { return "v1.19.0" }},
		{Priority: 3, Render: func() string { return "Main · 9G/100G" }},
		{Priority: 5, Render: func() string { return "3c" }},
	}
	sep := "  ·  "
	// Cumulative widths by priority join order: Mihari 6, +running 20, +conns 27,
	// +subscription 46, +version 58. Output is positional order.
	cases := []struct {
		name  string
		width int
		want  string
	}{
		{"fits-all", 80, "Mihari  ·  ● running  ·  v1.19.0  ·  Main · 9G/100G  ·  3c"},
		{"drops-version", 52, "Mihari  ·  ● running  ·  Main · 9G/100G  ·  3c"},
		{"drops-subscription", 40, "Mihari  ·  ● running  ·  3c"},
		{"drops-conns", 25, "Mihari  ·  ● running"},
		{"title-only", 10, "Mihari"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PriorityBar(tc.width, segments, sep)
			if got != tc.want {
				t.Fatalf("width=%d got %q want %q", tc.width, got, tc.want)
			}
		})
	}
}

func TestPriorityBar_EdgeCases(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if got := PriorityBar(50, nil, "  "); got != "" {
			t.Fatalf("got %q", got)
		}
		if got := PriorityBar(0, []PrioritySegment{{Priority: 1, Render: func() string { return "x" }}}, "  "); got != "" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("fallback-truncates", func(t *testing.T) {
		segments := []PrioritySegment{
			{Priority: 1, Render: func() string { return "very-long-segment" }},
		}
		got := PriorityBar(8, segments, "  ")
		if lipgloss.Width(got) != 8 || !strings.Contains(got, "…") {
			t.Fatalf("got %q (width %d)", got, lipgloss.Width(got))
		}
	})
	t.Run("always-kept-wins", func(t *testing.T) {
		segments := []PrioritySegment{
			{Priority: 1, Render: func() string { return "drop" }},
			{Priority: 0, Render: func() string { return "keep" }},
		}
		got := PriorityBar(4, segments, "  ")
		if got != "keep" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("render-once-per-frame", func(t *testing.T) {
		calls := 0
		segments := []PrioritySegment{
			{Priority: 2, Render: func() string { calls++; return "aa" }},
			{Priority: 1, Render: func() string { calls++; return "bb" }},
		}
		PriorityBar(100, segments, "  ")
		PriorityBar(100, segments, "  ")
		if calls != 4 { // two calls per frame, one per segment
			t.Fatalf("render calls=%d", calls)
		}
	})
	t.Run("positional-order-kept", func(t *testing.T) {
		// Droppable order (1 then 9) must not reorder the output.
		segments := []PrioritySegment{
			{Priority: 1, Render: func() string { return "low" }},
			{Priority: 9, Render: func() string { return "high" }},
		}
		got := PriorityBar(50, segments, "  ")
		if got != "low  high" {
			t.Fatalf("got %q", got)
		}
	})
}
