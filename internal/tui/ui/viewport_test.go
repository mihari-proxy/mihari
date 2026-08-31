package ui

import (
	"reflect"
	"strings"
	"testing"
)

func TestEnsureLineVisible(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                       string
		scrollY, viewH, n          int
		focusStart, focusEnd, want int
	}{
		{name: "viewH zero keeps scrollY", scrollY: 4, viewH: 0, n: 20, focusStart: 10, focusEnd: 11, want: 4},
		{name: "viewH negative keeps negative scrollY", scrollY: -3, viewH: -1, n: 20, focusStart: 0, focusEnd: 1, want: -3},
		{name: "empty lines returns 0", scrollY: 9, viewH: 5, n: 0, focusStart: 0, focusEnd: 1, want: 0},
		{name: "no focus clamps stale scrollY", scrollY: 40, viewH: 5, n: 20, focusStart: -1, focusEnd: 0, want: 15},
		{name: "inverted focus clamps", scrollY: 40, viewH: 5, n: 20, focusStart: 3, focusEnd: 3, want: 15},
		{name: "focus above window scrolls up", scrollY: 10, viewH: 5, n: 20, focusStart: 2, focusEnd: 3, want: 2},
		{name: "focus below window scrolls down", scrollY: 0, viewH: 5, n: 20, focusStart: 12, focusEnd: 13, want: 8},
		{name: "focus already visible unchanged", scrollY: 4, viewH: 5, n: 20, focusStart: 6, focusEnd: 7, want: 4},
		{name: "exclusive end already visible", scrollY: 0, viewH: 5, n: 20, focusStart: 4, focusEnd: 5, want: 0},
		{name: "tall block pins start then clamps", scrollY: 0, viewH: 5, n: 20, focusStart: 3, focusEnd: 12, want: 3},
		{name: "n shrinks below scrollY", scrollY: 50, viewH: 5, n: 8, focusStart: -1, focusEnd: 0, want: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EnsureLineVisible(tc.scrollY, tc.viewH, tc.n, tc.focusStart, tc.focusEnd)
			if got != tc.want {
				t.Fatalf("EnsureLineVisible(%d,%d,%d,%d,%d)=%d want %d",
					tc.scrollY, tc.viewH, tc.n, tc.focusStart, tc.focusEnd, got, tc.want)
			}
		})
	}
}

func TestSliceLines(t *testing.T) {
	t.Parallel()
	lines := []string{"a", "b", "c", "d", "e"}
	cases := []struct {
		name        string
		in          []string
		scrollY, h  int
		want        []string
		sameBacking bool
	}{
		{name: "height zero returns all", in: lines, scrollY: 2, h: 0, want: lines, sameBacking: true},
		{name: "height negative returns all", in: lines, scrollY: 2, h: -4, want: lines, sameBacking: true},
		{name: "height covers all", in: lines, scrollY: 0, h: 5, want: lines},
		{name: "height taller than n", in: lines, scrollY: 0, h: 9, want: lines},
		{name: "middle window", in: lines, scrollY: 1, h: 2, want: []string{"b", "c"}},
		{name: "scrollY past end clamps", in: lines, scrollY: 40, h: 2, want: []string{"d", "e"}},
		{name: "negative scrollY clamps to 0", in: lines, scrollY: -3, h: 2, want: []string{"a", "b"}},
		{name: "empty input", in: nil, scrollY: 3, h: 4, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SliceLines(tc.in, tc.scrollY, tc.h)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SliceLines(%v,%d,%d)=%v want %v", tc.in, tc.scrollY, tc.h, got, tc.want)
			}
			if tc.sameBacking && len(tc.in) > 0 && len(got) > 0 {
				tc.in[0] = "mutated"
				if got[0] != "mutated" && got[0] != "a" {
					t.Fatalf("unexpected aliasing %q", got[0])
				}
				tc.in[0] = "a"
			}
		})
	}
}

func TestLineViewportIsNotVisibleWindow(t *testing.T) {
	t.Parallel()
	start, end := VisibleWindow(20, 10, 0, true, 0)
	if start != 10 || end != 20 {
		t.Fatalf("VisibleWindow following got [%d,%d) want [10,20)", start, end)
	}
	if got := EnsureLineVisible(0, 10, 20, 9, 10); got != 0 {
		t.Fatalf("EnsureLineVisible exclusive-end=%d want 0", got)
	}
	lines := strings.Split("0 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19", " ")
	got := SliceLines(lines, 0, 10)
	if len(got) != 10 || got[0] != "0" || got[9] != "9" {
		t.Fatalf("SliceLines window=%v", got)
	}
}
