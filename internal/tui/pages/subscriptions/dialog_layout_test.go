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

// compactDetail opens an edit form with a fixed clock and synthetic catalog.
func compactDetail() *Model {
	now := time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC)
	m := New(nil, nil, func() time.Time { return now })
	p := protocol.Subscription{ID: "sample", Name: "Example", Enabled: true, Cached: true, AutoRefresh: true, UpdatedAt: now.Add(-time.Hour), ProxyMode: "proxy"}
	m.SetSize(100, 36)
	m.SetSubscriptions(protocol.SubscriptionList{ActiveID: p.ID, GlobalInterval: "12h", Subscriptions: []protocol.Subscription{p}})
	m.openForm(newEditForm(p), p.ID)
	return m
}

// TestDetailLayout_CompactSharedFields protects the single-row fields and two-row URL contract.
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

// dialogBorderHeight measures the box independently of its centered outer padding.
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

// TestDetailLayout_HeightFollowsContent rejects growth caused only by a taller terminal.
func TestDetailLayout_HeightFollowsContent(t *testing.T) {
	m := compactDetail()
	first := dialogBorderHeight(m.View())
	m.SetSize(100, 50)
	second := dialogBorderHeight(m.View())
	if first != second || second >= 30 {
		t.Fatalf("dialog stretched: %d -> %d", first, second)
	}
}

// TestDetailLayout_SaveAlwaysVisible keeps submission reachable below overflowing status.
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

// TestDetailLayout_GlobalIntervalIsOnlyPlaceholder distinguishes inherited values from edits.
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
	if strings.Contains(ansi.Strip(m.View()), "Leave blank to use global interval") {
		t.Fatal("long interval guidance should not occupy a separate row")
	}
	m.globalInterval = "4h"
	if !strings.Contains(ansi.Strip(m.View()), "Global · 4h") {
		t.Fatal("stale global interval")
	}
}

func TestDetailLayout_IntervalUnitsShareInputRow(t *testing.T) {
	m := compactDetail()
	m.form.inputs[2].SetValue("1h")
	m.form.move(2)
	for _, width := range []int{40, 54, 100} {
		m.SetSize(width, 20)
		layout := m.form.fieldLayout(m.theme, m.formTextWidth())
		field := layout.fields[2]
		found := false
		for _, line := range layout.lines[field.first : field.last+1] {
			plain := ansi.Strip(line)
			if strings.Contains(plain, "1h") && strings.Contains(plain, "ns/us/ms/s/m/h") {
				found = true
				if !strings.Contains(line, m.theme.Muted.Render("ns/us/ms/s/m/h")) {
					t.Fatal("units do not use muted styling")
				}
			}
			if lipgloss.Width(line) > m.formTextWidth() {
				t.Fatal("interval units overflow the field")
			}
		}
		if !found {
			t.Fatalf("width %d: units missing from input row", width)
		}
		if m.form.inputs[2].Value() != "1h" {
			t.Fatal("unit hint changed the draft")
		}
	}
}

// TestDetailStatus_CompactSummary keeps active selection independent of cache readiness.
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

// TestDetailTimestamp_LocalMinutes fixes the local timezone and missing-value contract.
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

// TestDetailLayout_ValidationErrorVisibleBelowLongStatus prevents hidden validation failures.
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

// TestDetailLayout_StatusRefreshKeepsFocusedField distinguishes focus tracking from manual scrolling.
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

// TestDetailLayout_InputAndCycleFocusStyles checks white text cursors and reversed cycle controls.
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
	for _, field := range []struct {
		index int
		value string
	}{
		{3, "‹ On ›"},
		{4, "‹ PROXY ›"},
		{5, "[ Disable ]"},
		{6, "In use"},
	} {
		m.form.index = field.index
		m.ensureFormFocus()
		if !strings.Contains(m.View(), m.theme.RowFocus.Render(field.value)) {
			t.Fatalf("%s must highlight only its option, excluding label and row padding", m.form.labels[field.index])
		}
	}
}

// TestDetailLayout_IntervalDraftAndWideInputSurviveResize protects draft and PATCH semantics.
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

// TestDetailLayout_SaveStatesFitSmallWindow keeps save feedback and cancellation inside the frame.
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

// TestDetailLayout_BlurredURLShowsOrigin checks clipping without changing the stored address.
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

// TestDetailLayout_ErrorWrapPreservesWords guards against splitting ordinary error text.
func TestDetailLayout_ErrorWrapPreservesWords(t *testing.T) {
	m := compactDetail()
	m.subscriptions[0].LastError = "Download failed. The cached configuration remains available."
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "available.") {
		t.Fatal("error wrapping split a word that fits on a line")
	}
}
