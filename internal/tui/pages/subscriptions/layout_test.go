package subscriptions

import (
	"fmt"
	"strings"
	"testing"
	"time"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

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

func TestSubscriptionWidths_NameFitsContentUpToLimit(t *testing.T) {
	for _, test := range []struct {
		name string
		want int
	}{
		{"abcdefghijklmnopqrst", 20},
		{strings.Repeat("订阅", 6), 24},
		{strings.Repeat("a", 80), 32},
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
				before, _, found := strings.Cut(line, "●")
				// The five-cell InUse column centers its dot after the two-cell gap.
				if !found || lipgloss.Width(before) != test.want+4 {
					t.Fatalf("active marker should align at the center of InUse: %q", line)
				}
			}
		})
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
