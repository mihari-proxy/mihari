package overview

import (
	"fmt"
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestOverview_SpeedRowsKeepRatesOnChartLine(t *testing.T) {
	for _, width := range []int{32, 40, 74, 79, 80, 81, 84, 90, 110, 140, 160} {
		for _, rates := range [][2]int64{
			{0, 0}, {999, 6656}, {121549, 6656}, {6656, 121549},
			{1023898, 6656}, {1048575, 6656}, {1 << 20, 1 << 30},
			{1<<63 - 1, 6656}, {6656, 1<<63 - 1},
		} {
			t.Run(fmt.Sprintf("width_%d_up_%d_down_%d", width, rates[0], rates[1]), func(t *testing.T) {
				model := New()
				model.SetSize(width, 30)
				model.SetSnapshot(Snapshot{Monitor: ui.MonitorSnapshot{
					UploadRate: rates[0], DownloadRate: rates[1],
					Traffic: []ui.TrafficPoint{{Up: 1, Down: 2}, {Up: 3, Down: 1}},
				}})
				view := stripANSI(model.View())
				for _, row := range []struct{ label, rate string }{
					{ui.MonitorUploadShort, ui.FormatRate(rates[0])},
					{ui.MonitorDownloadShort, ui.FormatRate(rates[1])},
				} {
					found := false
					for _, line := range strings.Split(view, "\n") {
						if !strings.Contains(line, row.label+" ") {
							continue
						}
						found = true
						if !strings.Contains(line, row.rate) {
							t.Fatalf("%s rate %q wrapped or clipped: %q", row.label, row.rate, line)
						}
						if lipgloss.Width(line) > width {
							t.Fatalf("speed row exceeds page width %d: %q", width, line)
						}
					}
					if !found {
						t.Fatalf("missing %s row:\n%s", row.label, view)
					}
				}
			})
		}
	}
}
