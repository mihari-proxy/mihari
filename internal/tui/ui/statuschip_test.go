package ui

import (
	"strings"
	"testing"
	"time"
)

func TestRenderStatusChip_UsesSemanticBackgrounds(t *testing.T) {
	theme := DefaultTheme()
	pending := RenderStatusChip(theme, StatusChipPending, SpinnerLabel(time.Unix(0, 0), "Installing"))
	done := RenderStatusChip(theme, StatusChipDone, DoneLabel)
	failed := RenderStatusChip(theme, StatusChipFailed, FailedLabel)

	if pending == "" || done == "" || failed == "" {
		t.Fatal("expected non-empty chips")
	}
	if pending == done || done == failed || pending == failed {
		t.Fatalf("chips must differ by kind: pending=%q done=%q failed=%q", pending, done, failed)
	}
	// Solid chips always emit ANSI; labels must remain readable as text.
	for _, sample := range []struct {
		name, chip, want string
	}{
		{"pending", pending, "Installing"},
		{"done", done, DoneLabel},
		{"failed", failed, FailedLabel},
	} {
		if !strings.Contains(sample.chip, sample.want) {
			t.Fatalf("%s missing label %q in %q", sample.name, sample.want, sample.chip)
		}
		if !strings.Contains(sample.chip, "\x1b[") {
			t.Fatalf("%s expected ANSI styling: %q", sample.name, sample.chip)
		}
	}
}

func TestRenderStatusChip_EmptyLabel(t *testing.T) {
	if got := RenderStatusChip(DefaultTheme(), StatusChipDone, ""); got != "" {
		t.Fatalf("got %q", got)
	}
}
