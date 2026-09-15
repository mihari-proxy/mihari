package subscriptions

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func compactDetail() *Model {
	now := time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC)
	m := New(nil, nil, func() time.Time { return now })
	p := protocol.Subscription{ID: "sample", Name: "Example", Enabled: true, Cached: true, AutoRefresh: true, UpdatedAt: now.Add(-time.Hour), ProxyMode: "proxy"}
	m.SetSize(100, 36)
	m.SetSubscriptions(protocol.SubscriptionList{ActiveID: p.ID, GlobalInterval: "12h", Subscriptions: []protocol.Subscription{p}})
	m.openForm(newEditForm(p), p.ID)
	return m
}

func TestDetailLayout_CompactSharedFields(t *testing.T) {
	for _, add := range []bool{false, true} {
		m := compactDetail()
		if add {
			m.openForm(newAddForm(), "")
			m.form.inputs[0].SetValue("Example")
		}
		m.form.reveal("https://example.test/subscription")
		lines := strings.Split(ansi.Strip(m.View()), "\n")
		for _, label := range []string{"Name", "Mode"} {
			value := "Example"
			if label == "Mode" {
				value = "‹"
			}
			found := false
			for _, line := range lines {
				if strings.Contains(line, label) && strings.Contains(line, value) {
					found = true
				}
			}
			if !found {
				t.Fatalf("add=%v: %s and value must share a row", add, label)
			}
		}
		for i, line := range lines {
			if strings.Contains(line, "URL") && (i+1 >= len(lines) || !strings.Contains(lines[i+1], "https://example.test/subscription")) {
				t.Fatal("URL needs its own following input row")
			}
		}
	}
}

func dialogBorderHeight(view string) int {
	start, end := -1, -1
	for i, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, "╭") {
			start = i
		}
		if strings.Contains(line, "╰") {
			end = i
		}
	}
	return end - start + 1
}

func TestDetailLayout_HeightFollowsContent(t *testing.T) {
	m := compactDetail()
	first := dialogBorderHeight(m.View())
	m.SetSize(100, 50)
	second := dialogBorderHeight(m.View())
	if first != second || second >= 30 {
		t.Fatalf("dialog stretched: %d -> %d", first, second)
	}
}

func TestDetailLayout_SaveAlwaysVisible(t *testing.T) {
	m := compactDetail()
	m.SetSize(54, 20)
	m.subscriptions[0].LastError = strings.Repeat("Long error details. ", 30)
	for i := 0; i <= len(m.form.inputs); i++ {
		view := ansi.Strip(m.View())
		if strings.Count(view, "[ Save ]") != 1 {
			t.Fatalf("Save missing or duplicated at field %d", i)
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
}

func TestDetailLayout_GlobalIntervalIsOnlyPlaceholder(t *testing.T) {
	m := compactDetail()
	m.globalInterval = "6h"
	if !strings.Contains(ansi.Strip(m.View()), "Global · 6h") {
		t.Fatal("missing actual global interval")
	}
	if m.form.inputs[2].Value() != "" || m.form.updateRequest("op", 1).Interval != nil {
		t.Fatal("placeholder changed draft")
	}
	m.form.move(1)
	m.form.move(1)
	if !strings.Contains(ansi.Strip(m.View()), "Leave blank to use global interval") {
		t.Fatal("missing focused interval guidance")
	}
	m.globalInterval = "4h"
	if !strings.Contains(ansi.Strip(m.View()), "Global · 4h") {
		t.Fatal("stale global interval")
	}
}

func TestDetailStatus_CompactSummary(t *testing.T) {
	m := compactDetail()
	view := ansi.Strip(m.formStatus())
	if !strings.HasPrefix(view, "● In use · Live · Enabled\n") {
		t.Fatalf("missing compact status: %q", view)
	}
	if !strings.Contains(view, "Cache") || !strings.Contains(view, "Available") || strings.Contains(view, "Last error") {
		t.Fatalf("incorrect cache/error presentation: %q", view)
	}
	m.subscriptions[0].CacheOutdated = true
	view = ansi.Strip(m.formStatus())
	if !strings.Contains(view, "In use · Outdated") {
		t.Fatalf("lost independent in-use state: %q", view)
	}
	m.activeID = ""
	m.subscriptions[0].LastError = "Download failed"
	view = ansi.Strip(m.formStatus())
	if !strings.Contains(view, "Not in use") || !strings.Contains(view, "Download failed") {
		t.Fatalf("missing inactive/error state: %q", view)
	}
}

func TestDetailTimestamp_LocalMinutes(t *testing.T) {
	prior := time.Local
	time.Local = time.FixedZone("test", 8*60*60)
	t.Cleanup(func() { time.Local = prior })
	if got := formatTimestamp(time.Date(2026, 9, 14, 19, 59, 22, 0, time.UTC)); got != "2026-09-15 03:59" {
		t.Fatalf("timestamp=%q", got)
	}
	if got := formatTimestamp(time.Time{}); got != "—" {
		t.Fatalf("zero timestamp=%q", got)
	}
}

func TestDetailLayout_ValidationErrorVisibleBelowLongStatus(t *testing.T) {
	m := compactDetail()
	m.SetSize(54, 20)
	m.subscriptions[0].LastError = strings.Repeat("Cached download failed. ", 30)
	m.form.inputs[0].SetValue("")
	m.form.index = len(m.form.inputs)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Name is required.") || !strings.Contains(view, "[ Save ]") {
		t.Fatalf("validation feedback lost\n%s", view)
	}
}

func TestDetailLayout_StatusRefreshKeepsFocusedField(t *testing.T) {
	m := compactDetail()
	m.SetSize(54, 20)
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	p := m.subscriptions[0]
	p.LastError = strings.Repeat("Cached download failed. ", 30)
	m.SetSubscriptions(protocol.SubscriptionList{GlobalInterval: "6h", ActiveID: p.ID, Subscriptions: []protocol.Subscription{p}})
	if !strings.Contains(ansi.Strip(m.View()), "URL") {
		t.Fatal("status refresh pushed the focused URL offscreen")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	before := m.dialogScroll
	m.SetSubscriptions(protocol.SubscriptionList{GlobalInterval: "6h", ActiveID: p.ID, Subscriptions: []protocol.Subscription{p}})
	if m.dialogScroll != before {
		t.Fatal("status refresh undid manual scrolling")
	}
}

func TestDetailLayout_InputAndCycleFocusStyles(t *testing.T) {
	m := compactDetail()
	styles := m.form.inputs[0].Styles()
	if styles.Focused.Text.GetReverse() || styles.Blurred.Text.GetReverse() {
		t.Fatal("text input uses row reverse")
	}
	if styles.Focused.Text.GetBackground() == nil {
		t.Fatal("focused input lacks its background")
	}
	r, g, b, _ := styles.Cursor.Color.RGBA()
	wr, wg, wb, _ := lipgloss.Color("15").RGBA()
	if r != wr || g != wg || b != wb {
		t.Fatal("input cursor is not white")
	}
	for i := 0; i < 4; i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	view := m.View()
	if !strings.Contains(view, "\x1b[7m") || !strings.Contains(ansi.Strip(view), "‹ PROXY ›") {
		t.Fatal("cycle field lacks reverse selection or arrows")
	}
}

func TestDetailLayout_IntervalDraftAndWideInputSurviveResize(t *testing.T) {
	m := compactDetail()
	url := "https://example.test/" + strings.Repeat("long-path/", 50)
	m.form.reveal(url)
	m.form.inputs[0].SetValue("示例订阅")
	m.form.inputs[2].SetValue("3h")
	for _, width := range []int{100, 54, 80} {
		m.SetSize(width, 20)
		m.View()
		if m.form.inputs[1].Value() != url || m.form.inputs[0].Value() != "示例订阅" {
			t.Fatal("resize damaged draft")
		}
		req := m.form.updateRequest("op", 7)
		if req.URL != nil || req.Interval == nil || *req.Interval != "3h" {
			t.Fatal("layout changed update semantics")
		}
	}
	m.form.baseline.Interval = "3h"
	m.form.inputs[2].SetValue("")
	m.View()
	req := m.form.updateRequest("op", 7)
	if req.Interval == nil || *req.Interval != "" {
		t.Fatal("clearing interval no longer inherits global")
	}
}

func TestDetailLayout_SaveStatesFitSmallWindow(t *testing.T) {
	m := compactDetail()
	m.SetSize(54, 20)
	for _, state := range []savePhase{saveSending, saveConflict, saveUnknown, saveRetryConfirm, saveChecking, saveRunning} {
		m.saveState = state
		m.dialogNote = "The configuration changed while you were editing. Overwrite your changed fields?"
		view := ansi.Strip(m.View())
		if strings.Contains(view, "[ Save ]") {
			t.Fatal("edit Save leaked into save state")
		}
		if len(strings.Split(view, "\n")) > 20 {
			t.Fatalf("save state %d overflowed", state)
		}
		if (state == saveConflict || state == saveRetryConfirm) && !strings.Contains(view, "[Cancel]") {
			t.Fatal("default Cancel clipped")
		}
	}
}

func TestDetailLayout_BlurredURLShowsOrigin(t *testing.T) {
	m := compactDetail()
	url := "https://example.test/" + strings.Repeat("sample-", 50)
	m.form.reveal(url)
	if !strings.Contains(ansi.Strip(m.View()), "https://example.test/") {
		t.Fatal("blurred URL hides its origin behind the cursor offset")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.form.inputs[1].Value() != url {
		t.Fatal("display clipping changed URL")
	}
}

func TestDetailLayout_ErrorWrapPreservesWords(t *testing.T) {
	m := compactDetail()
	m.subscriptions[0].LastError = "Download failed. The cached configuration remains available."
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "available.") {
		t.Fatal("error wrapping split a word that fits on a line")
	}
}
