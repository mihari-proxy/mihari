package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestDiagnosticsLayout_BothPanesAndCompleteFrame(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {100, 28}, {72, 22}, {50, 25}, {60, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			w := newDiagnosticWindow()
			w.add(protocol.Diagnostic{ID: "fixture:1", Time: time.Date(2026, 9, 17, 11, 25, 53, 0, time.UTC), Severity: "error", Component: "web", Event: "websocket.relay.failed", Summary: "upstream failed", Detail: "original cause", State: protocol.DiagnosticAvailable}, ui.PageOverview)
			w.show(context.Background(), ui.PageOverview, "")
			for _, focus := range []bool{false, true} {
				w.detailFocus = focus
				view := w.view(size[0], size[1])
				plain := normalizeRender(view)
				for _, want := range []string{"Records", "Details", "upstream failed", "original cause", "Esc", "╰", "╯"} {
					if !strings.Contains(plain, want) {
						t.Errorf("focus=%v missing %q in\n%s", focus, want, plain)
					}
				}
				if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
					t.Fatalf("render exceeds terminal: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
				}
			}
		})
	}
}

func TestDiagnosticsLayout_DetailMetadataAndDuplicateSummary(t *testing.T) {
	w := newDiagnosticWindow()
	w.add(protocol.Diagnostic{ID: "fixture:1", Time: time.Date(2026, 9, 17, 11, 25, 53, 0, time.UTC), Severity: "error", Component: "web", Event: "websocket.relay.failed", Summary: "same failure", Detail: "same failure", State: protocol.DiagnosticAvailable}, ui.PageOverview)
	w.show(context.Background(), ui.PageOverview, "")
	detail := strings.Join(w.detailLines(140), "\n")
	if strings.Count(detail, "same failure") != 1 {
		t.Fatalf("duplicate summary in details: %s", detail)
	}
	for _, want := range []string{"2026-09-17", "11:25:53", "websocket.relay.failed"} {
		if !strings.Contains(detail, want) {
			t.Errorf("missing %s in detail: %s", want, detail)
		}
	}
}

func TestDiagnosticsLayout_NarrowScrollReachesLastLine(t *testing.T) {
	model := NewModel()
	model.width, model.height = 50, 20
	record := protocol.Diagnostic{ID: "fixture:1", Summary: "fixture repeated", Detail: strings.Repeat("长诊断消息 mixed English words\n", 40) + "LAST-LINE", State: protocol.DiagnosticAvailable}
	model.recordDiagnostic(nil, &record, ui.PageOverview)
	for _, key := range []rune{tea.KeyF2, tea.KeyTab, tea.KeyEnd} {
		next, _ := model.Update(diagnosticKey(key))
		model = next.(Model)
	}
	if view := normalizeRender(model.View().Content); !strings.Contains(view, "LAST-LINE") || !strings.Contains(view, "fixture repeated") {
		t.Fatalf("narrow details cannot reach end while retaining list:\n%s", view)
	}
}

func TestDiagnosticsLayout_RepeatedRecordsAndLiveSelection(t *testing.T) {
	w := newDiagnosticWindow()
	for i := range 4 {
		w.add(protocol.Diagnostic{ID: fmt.Sprintf("fixture:%d", i), Time: time.Unix(int64(i+1), 0), Summary: "same failure", Detail: "same failure", State: protocol.DiagnosticAvailable}, ui.PageOverview)
	}
	w.show(context.Background(), ui.PageOverview, "fixture:1")
	w.scroll = 1
	w.add(protocol.Diagnostic{ID: "fixture:new", Time: time.Unix(10, 0), Summary: "same failure", Detail: "same failure", State: protocol.DiagnosticAvailable}, ui.PageOverview)
	if len(w.entries) != 5 || w.selected != "fixture:1" || w.pinned.ID != "fixture:1" || w.scroll != 1 {
		t.Fatal("repeated records were grouped or a new record interrupted reading")
	}
	if view := normalizeRender(w.view(100, 28)); !strings.Contains(view, "5 records") || !strings.Contains(view, "Records 4/5") {
		t.Fatalf("record positions are missing:\n%s", view)
	}
}

func TestDiagnosticsLayout_ShortHistoryAvoidsEmptyRows(t *testing.T) {
	w := newDiagnosticWindow()
	w.add(protocol.Diagnostic{ID: "fixture:1", Summary: "short failure", Detail: "short failure", State: protocol.DiagnosticAvailable}, ui.PageOverview)
	w.show(context.Background(), ui.PageOverview, "")
	for _, width := range []int{120, 60} {
		view := normalizeRender(w.view(width, 40))
		lines := strings.Split(view, "\n")
		top, bottom := -1, -1
		for i, line := range lines {
			if strings.Contains(line, "╭") {
				top = i
			}
			if strings.Contains(line, "╰") {
				bottom = i
			}
		}
		if top < 0 || bottom < top || bottom-top+1 > 14 {
			t.Errorf("short history leaves an unnecessarily tall dialog (%d rows)", bottom-top+1)
		}
	}
}

func TestDiagnosticsLayout_NoticesAndLongTextStayBounded(t *testing.T) {
	w := newDiagnosticWindow()
	w.add(protocol.Diagnostic{ID: "fixture:1", Severity: "warning", Component: strings.Repeat("来源", 30), Summary: strings.Repeat("长摘要", 50), Detail: strings.Repeat("错误\x1b[31m\t", 80), State: protocol.DiagnosticExpired, Truncated: true, TruncationReason: "fixture limit"}, ui.PageOverview)
	w.show(context.Background(), ui.PageOverview, "")
	w.copyStatus = "Copy failed"
	w.remoteNotice = "Older daemon diagnostic records were evicted"
	for _, size := range [][2]int{{100, 28}, {60, 12}, {30, 12}, {50, 10}} {
		view := w.view(size[0], size[1])
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Errorf("%dx%d overflowed to %dx%d", size[0], size[1], lipgloss.Width(view), lipgloss.Height(view))
		}
	}
	detail := strings.Join(w.detailLines(100), "\n")
	for _, want := range []string{"expired", "truncated", `\x1b[31m`, "Summary:"} {
		if !strings.Contains(detail, want) {
			t.Errorf("lost %q in diagnostic details", want)
		}
	}
	if strings.Contains(detail, "\x1b[31m") {
		t.Fatal("raw terminal control sequence entered details")
	}
}

func TestGoldenDiagnostics(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{{"wide", 120, 28}, {"narrow", 60, 24}, {"short", 60, 12}} {
		t.Run(size.name, func(t *testing.T) {
			w := newDiagnosticWindow()
			for i := range 4 {
				w.add(protocol.Diagnostic{
					ID: fmt.Sprintf("fixture:%d", i), Time: time.Date(2026, 9, 17, 11, 25, 50+i, 0, time.UTC),
					Severity: "error", Component: "web", Event: "websocket.relay.failed",
					Summary: "failed to read: websocket: message too big: read limited at 32769 bytes",
					Detail:  "failed to read: websocket: message too big: read limited at 32769 bytes", State: protocol.DiagnosticAvailable,
				}, ui.PageOverview)
			}
			w.show(context.Background(), ui.PageOverview, "fixture:2")
			assertGoldenContent(t, "diagnostics_"+size.name, strings.Trim(trimRenderPadding(normalizeRender(w.view(size.width, size.height))), "\n"))
		})
	}
}
