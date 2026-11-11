package subscriptions

import (
	"fmt"
	"strings"
	"testing"
	"time"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestSubscriptionWidths_FixedLiteralsRemainComplete(t *testing.T) {
	clock := time.Unix(100, 0)
	literals := map[string][]string{
		"active":      {"●", ""},
		"state":       {ui.EnabledLabel, ui.DisabledLabel},
		"proxy":       {proxyModeLabel(""), proxyModeLabel("proxy"), proxyModeLabel("auto"), "Local file"},
		"traffic":     {ui.MissingValue},
		"lastSuccess": {ui.MissingValue, "now"},
		"nextRefresh": {ui.DisabledLabel, ui.ManualLabel, ui.RetryPendingLabel, "now"},
	}
	for _, phase := range []loadPhase{
		loadDisabled, loadMissing, loadFetching, loadApplying, loadWorking,
		loadCached, loadStale, loadFailed, loadLive, loadOutdated, loadExpired,
	} {
		label, _ := loadPhaseLabel(phase, clock)
		literals["load"] = append(literals["load"], label)
	}
	for _, mode := range []string{"", "proxy", "auto", "file"} {
		for _, width := range []int{40, 58, 72, 84, 100, 120, 200, 320} {
			t.Run(fmt.Sprintf("%s/%d", mode, width), func(t *testing.T) {
				m := New(nil, nil, func() time.Time { return clock })
				m.SetSize(width, 20)
				sub := protocol.Subscription{ID: "a", Name: "fixture", Enabled: true, AutoRefresh: true, ProxyMode: mode}
				if mode == "file" {
					sub.SourceType, sub.ProxyMode = "file", ""
				}
				m.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{sub}})
				cols, widths := m.subscriptionWidths()
				if width >= 200 && len(cols) != 8 {
					t.Fatalf("wide screen lost columns: %+v", cols)
				}
				for i, col := range cols {
					values := append([]string{col.Title}, literals[col.ID]...)
					if col.ID == "proxy" {
						// Mode sizes to the catalog's current values.
						values = []string{col.Title, rowFrom(sub, false, "", clock, clock, "").proxy}
					}
					for _, value := range values {
						if got := strings.TrimSpace(ansi.Strip(ui.PadCell(value, widths[i], col.Align))); got != value {
							t.Errorf("%s width %d truncates %q to %q", col.ID, widths[i], value, got)
						}
					}
				}
				view := ansi.Strip(m.View())
				if width >= 200 && !strings.Contains(view, ui.RetryPendingLabel) {
					t.Errorf("retry label missing from rendered page:\n%s", view)
				}
				for _, line := range strings.Split(view, "\n") {
					if lipgloss.Width(line) > width {
						t.Errorf("page overflows width %d: %q", width, line)
					}
				}
			})
		}
	}
}

func TestSubscriptionWidths_ContentKeepsWideScreenColumnsTogether(t *testing.T) {
	for _, width := range []int{120, 200, 320} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			model := New(nil, nil, time.Now)
			model.SetSize(width, 20)
			model.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{
				{ID: "a", Name: "kanata", Download: 53 << 30, Total: 80 << 30},
			}})
			cols, widths := model.subscriptionWidths()
			if len(cols) != 8 {
				t.Fatalf("wide screen should retain all columns, got %d", len(cols))
			}
			for i, col := range cols {
				if col.ID == "name" && widths[i] != 10 {
					t.Errorf("short name should use 10 columns, got %d", widths[i])
				}
				if col.ID == "traffic" && widths[i] != 11 {
					t.Errorf("short traffic should use 11 columns, got %d", widths[i])
				}
			}
		})
	}
}

// TestSubscriptionWidths_NameFitsContentUpToLimit checks the display-cell cap,
// ellipsis truncation, and alignment of the following InUse column.
func TestSubscriptionWidths_NameFitsContentUpToLimit(t *testing.T) {
	for _, test := range []struct {
		name string
		want int
	}{
		{"abcdefghijklmnopqrst", 20},
		{strings.Repeat("订阅", 6), 24},
		{strings.Repeat("a", 36), 36},
		{strings.Repeat("a", 40), 40},
		{strings.Repeat("a", 80), 40},
		{strings.Repeat("订阅", 20), 40},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := New(nil, nil, time.Now)
			model.SetSize(200, 20)
			model.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{
				{ID: "a", Name: "short"}, {ID: "b", Name: test.name},
			}})
			cols, widths := model.subscriptionWidths()
			if widths[0] != test.want {
				t.Fatalf("name width=%d, want %d", widths[0], test.want)
			}
			for _, name := range []string{"short", test.name} {
				line := (row{name: name, active: "●"}).Render(ui.DefaultTheme(), cols, widths)
				if lipgloss.Width(name) > test.want && !strings.Contains(line, "…") {
					t.Fatalf("long name should end with an ellipsis: %q", line)
				}
				before, _, found := strings.Cut(line, "●")
				// The five-cell InUse column centers its dot after the two-cell gap.
				if !found || lipgloss.Width(before) != test.want+4 {
					t.Fatalf("active marker should align at the center of InUse: %q", line)
				}
			}
		})
	}
}

// TestModel_ListRuleFillsSectionWidth verifies that compact columns leave no gap
// between the header rule and the section's right padding.
func TestModel_ListRuleFillsSectionWidth(t *testing.T) {
	for _, width := range []int{72, 120, 200, 320} {
		for _, name := range []string{"", "kanata", strings.Repeat("订阅", 30)} {
			t.Run(fmt.Sprintf("%d/%s", width, name), func(t *testing.T) {
				model := New(nil, nil, time.Now)
				model.SetSize(width, 20)
				if name != "" {
					model.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "a", Name: name}}})
				}
				lines := strings.Split(ansi.Strip(model.View()), "\n")
				// Keep the marker gutter at the left and one padding cell at the right.
				want := "│   " + strings.Repeat("─", width-8) + " │"
				if lines[2] != want {
					t.Fatalf("rule does not fill the section:\n got %q\nwant %q", lines[2], want)
				}
				for _, line := range lines {
					if got := lipgloss.Width(line); got != width-2 {
						t.Fatalf("section line width=%d, want %d: %q", got, width-2, line)
					}
				}
			})
		}
	}
}

// TestModel_ListFocusFillsSectionWidth checks highlight boundaries and ensures
// that only the focused row is highlighted while the page owns keyboard focus.
func TestModel_ListFocusFillsSectionWidth(t *testing.T) {
	for _, width := range []int{72, 120, 200, 320} {
		for _, name := range []string{"kanata", strings.Repeat("订阅", 30)} {
			t.Run(fmt.Sprintf("%d/%s", width, name), func(t *testing.T) {
				model := New(nil, nil, time.Now)
				model.SetSize(width, 20)
				model.SetSubscriptions(protocol.SubscriptionList{ActiveID: "a", Subscriptions: []protocol.Subscription{
					{ID: "a", Name: name, Enabled: true, Cached: true},
					{ID: "b", Name: "other"},
				}})
				model.FocusFirst()
				model.SetContentFocused(true)
				lines := strings.Split(model.View(), "\n")
				selected := lines[3]
				start := strings.Index(selected, "\x1b[7m")
				last := strings.LastIndex(selected, "\x1b[7m")
				if start < 0 || lipgloss.Width(selected[:start]) != 2 {
					t.Fatalf("focus should start at the row marker: %q", selected)
				}
				span, _, closed := strings.Cut(selected[last:], "\x1b[m")
				if !closed || lipgloss.Width(selected[:last]+span) != width-4 {
					t.Fatalf("focus should reach the right padding: %q", selected)
				}
				if strings.Contains(lines[4], "\x1b[7m") {
					t.Fatalf("unselected row should not be highlighted: %q", lines[4])
				}
				model.SetContentFocused(false)
				if strings.Contains(model.View(), "\x1b[7m") {
					t.Fatal("returning focus to the rail should clear the row highlight")
				}
			})
		}
	}
}

func TestSubscriptionWidths_NarrowScreenKeepsPriorityAndFits(t *testing.T) {
	model := New(nil, nil, time.Now)
	model.SetSize(72, 20)
	model.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{
		{ID: "a", Name: strings.Repeat("long", 20)},
	}})
	cols, widths := model.subscriptionWidths()
	if len(cols) >= 8 || cols[0].ID != "name" || cols[1].ID != "active" {
		t.Fatalf("narrow screen should retain name and active while dropping secondary columns: %+v", cols)
	}
	used := 2 + 2*(len(cols)-1)
	for _, width := range widths {
		used += width
	}
	if budget := ui.SectionTextWidth(ui.FullSectionInner(72)); used > budget {
		t.Fatalf("columns use %d cells, budget=%d", used, budget)
	}
}
