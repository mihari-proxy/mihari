package tui

import (
	"testing"

	"github.com/LeeShunEE/mihari/internal/tui/ui"
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
