package ui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestPreparedOverlay_RepeatedFramesAvoidCellBufferAllocations(t *testing.T) {
	theme := DefaultTheme()
	background := strings.Repeat(strings.Repeat("X", 160)+"\n", 50)
	overlay := PrepareOverlay(theme, background, 160, 50)
	dialog := theme.Dialog.Width(90).Render("Synthetic input")
	result := testing.Benchmark(func(b *testing.B) {
		for b.Loop() {
			overlay.Center(dialog)
		}
	})
	// 160x50 text needs about 8 KiB. A 64 KiB budget permits row/string
	// bookkeeping while rejecting a full grid of styled terminal cells.
	if result.AllocedBytesPerOp() > 64*1024 {
		t.Fatalf("frame exceeds row-rendering allocation budget: %d bytes", result.AllocedBytesPerOp())
	}
}

func TestPreparedOverlay_PreservesOpaqueUnicodeAndColors(t *testing.T) {
	theme := DefaultTheme()
	for _, background := range []string{strings.Repeat(strings.Repeat("X", 80)+"\n", 24), strings.Repeat(strings.Repeat("界", 40)+"\n", 24)} {
		for _, size := range []int{30, 31} {
			dialog := theme.Dialog.Width(size).Render(theme.Title.Render("编辑 user"))
			got := PrepareOverlay(theme, background, 80, 24).Center(dialog)
			want := CenterOverlay(theme, background, dialog, 80, 24)
			if ansi.Strip(got) != ansi.Strip(want) {
				a, z := strings.Split(ansi.Strip(got), "\n"), strings.Split(ansi.Strip(want), "\n")
				for i := range min(len(a), len(z)) {
					if a[i] != z[i] {
						t.Fatalf("overlay width %d line %d: got %q want %q", size, i, a[i], z[i])
					}
				}
				t.Fatalf("overlay height: got %d want %d", len(a), len(z))
			}
			if lipgloss.Width(got) > 80 || lipgloss.Height(got) > 24 {
				t.Fatal("overlay exceeded the viewport")
			}
			firstBackground := strings.Split(strings.TrimSuffix(background, "\n"), "\n")[0]
			if strings.Split(got, "\n")[0] != theme.Muted.Render(firstBackground) || !strings.Contains(got, theme.Title.Render("编辑 user")) {
				t.Fatal("background row or dialog title lost its assigned color")
			}
		}
	}
}
