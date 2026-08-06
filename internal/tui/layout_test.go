package tui

import (
	"testing"

	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		width  int
		height int
		want   ui.SizeClass
	}{
		{100, 28, ui.Full},
		{72, 22, ui.Compact},
		{99, 28, ui.Compact},
		{100, 22, ui.Compact},
		{71, 22, ui.TooSmall},
		{72, 21, ui.TooSmall},
	}
	for _, test := range tests {
		if got := Classify(test.width, test.height); got != test.want {
			t.Fatalf("%dx%d=%v want=%v", test.width, test.height, got, test.want)
		}
	}
}

func TestCalculateLayout_FullHasStatusAndNarrowRail(t *testing.T) {
	l := calculateLayout(100, 28)
	if l.Class != ui.Full {
		t.Fatalf("class %v", l.Class)
	}
	if l.RailWidth != 16 {
		t.Fatalf("rail %d", l.RailWidth)
	}
	if l.StatusHeight != 1 {
		t.Fatal("status row")
	}
	if l.FooterHeight != 1 {
		t.Fatalf("footer %d", l.FooterHeight)
	}
	if l.ContentWidth != 100-16 {
		t.Fatalf("content width %d", l.ContentWidth)
	}
	// ContentHeight = height - StatusHeight - FooterHeight
	if l.ContentHeight != 28-1-1 {
		t.Fatalf("content height %d", l.ContentHeight)
	}
	// large monitor removed: MonitorHeight is 0 or at most 1
	if l.MonitorHeight < 0 || l.MonitorHeight > 1 {
		t.Fatalf("monitor height %d want 0 or 1", l.MonitorHeight)
	}
	if l.RailNavHeight != l.ContentHeight-l.MonitorHeight {
		t.Fatalf("rail nav %d want %d", l.RailNavHeight, l.ContentHeight-l.MonitorHeight)
	}
}

func TestCalculateLayout_CompactRail14(t *testing.T) {
	l := calculateLayout(80, 24)
	if l.Class != ui.Compact {
		t.Fatalf("class %v", l.Class)
	}
	if l.RailWidth != 14 {
		t.Fatalf("rail %d", l.RailWidth)
	}
	if l.StatusHeight != 1 {
		t.Fatal("status row")
	}
	if l.MonitorHeight != 0 {
		t.Fatalf("monitor height %d", l.MonitorHeight)
	}
	if l.ContentWidth != 80-14 {
		t.Fatalf("content width %d", l.ContentWidth)
	}
	if l.ContentHeight != 24-1-1 {
		t.Fatalf("content height %d", l.ContentHeight)
	}
	if l.RailNavHeight != l.ContentHeight {
		t.Fatalf("rail nav %d", l.RailNavHeight)
	}
}

func TestCalculateLayout_TooSmallHasNoChrome(t *testing.T) {
	l := calculateLayout(71, 22)
	if l.Class != ui.TooSmall {
		t.Fatalf("class %v", l.Class)
	}
	if l.StatusHeight != 0 {
		t.Fatalf("status height %d", l.StatusHeight)
	}
	if l.RailWidth != 0 || l.ContentWidth != 0 || l.ContentHeight != 0 {
		t.Fatalf("unexpected chrome on too-small: %+v", l)
	}
}
