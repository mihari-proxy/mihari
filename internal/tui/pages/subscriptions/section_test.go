package subscriptions

import (
	"strings"
	"testing"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestView_SingleSectionFraming(t *testing.T) {
	model := New(nil, nil, func() time.Time { return time.Unix(100, 0) })
	model.SetSize(120, 24)
	model.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{
		{ID: "a", Name: "Alpha", Enabled: true},
	}})
	view := model.View()
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╰") {
		t.Fatalf("missing section borders:\n%s", view)
	}
	want := ui.FormatSubscriptionsTitle(1)
	if !strings.Contains(view, want) {
		t.Fatalf("missing title %q:\n%s", want, view)
	}
	if !strings.Contains(view, "Alpha") {
		t.Fatalf("row missing:\n%s", view)
	}
}
