package subscriptions

import (
	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"strings"
	"testing"
	"time"
)

func TestDetailStatus_PriorityAndSchedule(t *testing.T) {
	now := time.Unix(100000, 0).UTC()
	p := protocol.Subscription{Enabled: true, Cached: true, AutoRefresh: true, UpdatedAt: now.Add(-20 * time.Hour), ScheduleFrom: now, Interval: "6h", IntervalRefreshRequired: true}
	for _, tc := range []struct {
		name   string
		mutate func(*protocol.Subscription)
		want   string
	}{
		{"interval", func(p *protocol.Subscription) {}, "Expired"},
		{"manual interval", func(p *protocol.Subscription) { p.AutoRefresh = false }, "Expired"},
		{"source", func(p *protocol.Subscription) { p.CacheOutdated = true }, "Outdated"},
		{"missing", func(p *protocol.Subscription) { p.Cached = false; p.CacheOutdated = true }, "Missing"},
		{"failure", func(p *protocol.Subscription) { p.LastError = "failure"; p.CacheOutdated = true }, "Failed"},
		{"disabled", func(p *protocol.Subscription) { p.Enabled = false; p.LastError = "failure" }, "Disabled"},
		{"manual age", func(p *protocol.Subscription) { p.IntervalRefreshRequired = false; p.AutoRefresh = false }, "Need refresh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := p
			tc.mutate(&q)
			label, _ := loadPhaseLabel(resolveLoadPhase(q, true, "", now, "12h"), now)
			if label != tc.want {
				t.Fatalf("status=%s want %s", label, tc.want)
			}
		})
	}
	if next := nextRefreshLabel(p, now, "12h"); next != "in 6h" {
		t.Fatalf("next=%s", next)
	}
	p.AutoRefresh = false
	if nextRefreshLabel(p, now, "12h") != "Manual" {
		t.Fatal("manual next is not Manual")
	}
}

// TestDetailForm_ChangedFieldsAndSaveFocus checks PATCH field selection and Save navigation.
func TestDetailForm_ChangedFieldsAndSaveFocus(t *testing.T) {
	f := newEditForm(protocol.Subscription{Name: "Main", Interval: "6h", AutoRefresh: true, ProxyMode: "auto"})
	req := f.updateRequest("op", 7)
	if req.Name != nil || req.Interval != nil || req.AutoRefresh != nil || req.ProxyMode != nil || req.URL != nil {
		t.Fatal("unchanged fields sent")
	}
	f.inputs[0].SetValue("Renamed")
	req = f.updateRequest("op", 7)
	if req.Name == nil || *req.Name != "Renamed" || req.Interval != nil || req.ProxyMode != nil {
		t.Fatal("patch did not isolate changed name")
	}
	if len(f.inputs) != 5 {
		t.Fatalf("fields=%d", len(f.inputs))
	}
	for i := 0; i < len(f.inputs); i++ {
		f.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m := New(nil, nil, nil)
	m.openForm(f, "")
	if f.index != len(f.inputs) || !strings.Contains(m.View(), "[ Save ]") {
		t.Fatal("Save lacks separate focus")
	}
}

func TestDetailForm_CycleFieldsDoNotSubmit(t *testing.T) {
	f := newAddForm()
	f.move(1)
	f.move(1)
	f.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	if req := f.addRequest("op", 1); req.ProxyMode != "proxy" {
		t.Fatal("mode did not cycle")
	}
	f.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if req := f.addRequest("op", 1); req.ProxyMode != "" {
		t.Fatal("mode did not cycle backwards")
	}
}
