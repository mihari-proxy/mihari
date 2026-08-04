package ui

import (
	"testing"
	"time"
)

func TestSpinner_FrameCyclesBraille(t *testing.T) {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	base := time.Unix(0, 0).UTC()
	for i, want := range frames {
		got := SpinnerFrame(base.Add(time.Duration(i)*100*time.Millisecond), 100*time.Millisecond)
		if got != want {
			t.Fatalf("i=%d got %q want %q", i, got, want)
		}
	}
}

func TestSpinner_Label(t *testing.T) {
	if SpinnerLabel(time.Unix(0, 0), "Working…") != "⠋ Working…" {
		t.Fatal("expected frame + space + label")
	}
}
