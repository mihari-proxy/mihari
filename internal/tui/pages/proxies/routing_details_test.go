package proxies

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// newRoutingDetailsModel supplies matching routing and candidate revisions so
// both rows offer actions instead of displaying stale-state explanations.
func newRoutingDetailsModel() *Model {
	m := New(nil, nil)
	m.SetSize(100, 22)
	m.SetContentFocused(true)
	m.SetRoutingAvailable(true, 1)
	revision := uint64(3)
	m.SetRouting(protocol.RoutingStatus{DesiredMode: "rule", State: "applied", Revision: revision, SubscriptionID: "sub", GlobalSelection: "Tokyo"}, 1)
	m.SetGroups(protocol.ProxyGroups{Revision: &revision, SubscriptionID: "sub", Groups: []protocol.ProxyGroup{{Name: "GLOBAL", Now: "Tokyo", Nodes: []protocol.ProxyNode{{Name: "Tokyo"}}}}})
	return m
}

// TestLocateHeader_SelectedLabel pins the unboxed action beside the selected name.
func TestLocateHeader_SelectedLabel(t *testing.T) {
	m, _ := newLocateModel()
	line := ansi.Strip(m.renderGroupHeader(m.groups[0], 74, true))
	if !strings.Contains(line, "Now: two  → Jump to Selected") || strings.Contains(line, "[Locate]") {
		t.Fatalf("unexpected locate label: %q", line)
	}
}

// TestRoutingHeader_FocusedActionHint limits inline hints to the active content row.
func TestRoutingHeader_FocusedActionHint(t *testing.T) {
	for _, focused := range []bool{true, false} {
		for _, row := range []int{0, 1, -1} {
			t.Run(fmt.Sprintf("content=%v/row=%d", focused, row), func(t *testing.T) {
				m := newRoutingDetailsModel()
				m.SetContentFocused(focused)
				m.routing.focus = row
				header := m.routingHeader()
				for i, suffix := range []string{"Rule · Press Enter to Change", "Tokyo · Press Enter to Select"} {
					plain := ansi.Strip(header[i+1])
					want := focused && row == i
					if strings.Contains(plain, suffix) != want || strings.Contains(plain, "Enter") != want || strings.Contains(plain, "›") != want {
						t.Fatalf("hint/focus mismatch: %q; focused=%v", plain, want)
					}
				}
			})
		}
	}
}

// TestRoutingHeader_ActionHintWidthPriority prevents optional hints from shortening
// values and checks the exact column at which each complete hint fits.
func TestRoutingHeader_ActionHintWidthPriority(t *testing.T) {
	for _, test := range []struct {
		row, valueWidth int
		value           string
	}{{0, 4, "Rule"}, {1, 5, "Tokyo"}, {1, 6, "香港🌏"}} {
		row, value := test.row, test.value
		suffix := []string{" · Press Enter to Change", " · Press Enter to Select"}[row]
		// Page chrome consumes six columns; the marker and aligned label use eleven.
		boundary := 6 + 11 + test.valueWidth + lipgloss.Width(suffix)
		for _, width := range []int{30, boundary - 1, boundary, boundary + 1, 58, 80, 160} {
			t.Run(fmt.Sprintf("value=%s/width=%d", value, width), func(t *testing.T) {
				m := newRoutingDetailsModel()
				m.SetSize(width, 22)
				m.routing.focus = row
				if row == 1 {
					m.routing.status.GlobalSelection = value
				}
				header := m.routingHeader()
				plain := ansi.Strip(header[row+1])
				wantHint := width >= boundary
				want := fmt.Sprintf("› %-9s%s", []string{"Mode", "GLOBAL"}[row], value)
				if wantHint {
					want += suffix
				}
				// Check the actual content, not the width enforced by the section
				// painter: a clipped partial hint must not pass at narrow widths.
				if got := strings.TrimSpace(strings.Trim(plain, "│")); got != want {
					t.Fatalf("row content=%q; want %q", got, want)
				}
			})
		}
	}
	for _, width := range []int{30, 58, 80, 160} {
		m := newRoutingDetailsModel()
		m.SetSize(width, 22)
		m.routing.focus = 1
		m.routing.status.GlobalSelection = strings.Repeat("香港🌏Long-node", 20)
		line := ansi.Strip(m.routingHeader()[2])
		value := ui.TruncateVisible(m.routing.status.GlobalSelection, ui.SectionTextWidth(ui.FullSectionInner(width))-11)
		if !strings.Contains(line, value) || strings.Contains(line, "Press Enter") || strings.Contains(line, " · ") {
			t.Fatalf("long value lost space to action hint: %q", line)
		}
	}
}

// TestRoutingHeader_StatusNotesRemainVisible keeps unavailable-state explanations
// independent of keyboard focus and mutually exclusive with the row's action hint.
func TestRoutingHeader_StatusNotesRemainVisible(t *testing.T) {
	for _, state := range []string{"pending", "unknown", "unconfirmed", "stale"} {
		for _, focused := range []bool{true, false} {
			for _, row := range []int{-1, 0, 1} {
				t.Run(fmt.Sprintf("%s/content=%v/row=%d", state, focused, row), func(t *testing.T) {
					m := newRoutingDetailsModel()
					m.SetContentFocused(focused)
					m.routing.focus = row
					m.routing.status.Message = "Saved mode will apply when the core starts"
					modeNote := ""
					switch state {
					case "pending":
						m.routing.status.State = "pending"
						modeNote = "Saved · pending"
					case "unknown":
						m.routing.status.State = "unknown"
						modeNote = "Live state unavailable"
					case "unconfirmed":
						m.routing.known = false
						modeNote = "Live state unavailable"
					case "stale":
						m.InvalidateGroups()
					}
					header := m.routingHeader()
					if modeNote != "" && (!strings.Contains(ansi.Strip(header[1]), modeNote) || strings.Contains(header[1], "Enter")) {
						t.Fatalf("mode status missing or replaced: %q", header[1])
					}
					if !m.globalCandidatesCurrent() && (!strings.Contains(ansi.Strip(header[2]), "Waiting for candidates") || strings.Contains(header[2], "Enter")) {
						t.Fatalf("candidate status missing or replaced: %q", header[2])
					}
					if len(header) != 5 || !strings.Contains(ansi.Strip(header[3]), m.routing.status.Message) {
						t.Fatal("lost extra status message")
					}
				})
			}
		}
	}
}

// TestRoutingHeader_SegmentedFocusColors verifies the displayed colors across
// inline style resets, including the hint immediately after a reversed value.
func TestRoutingHeader_SegmentedFocusColors(t *testing.T) {
	for _, focused := range []bool{false, true} {
		for _, row := range []int{0, 1} {
			m := newRoutingDetailsModel()
			m.SetContentFocused(focused)
			m.routing.focus = row
			header := m.routingHeader()
			for i, label := range []string{"Mode", "GLOBAL"} {
				fg, bg, valueFG, valueBG := 7, 0, 78, 0
				if focused && row == i {
					fg, bg, valueFG, valueBG = 0, 7, 0, 78
					assertRoutingColors(t, header[i+1], "› ", fg, bg)
				}
				assertRoutingColors(t, header[i+1], fmt.Sprintf("%-9s", label), fg, bg)
				assertRoutingColors(t, header[i+1], []string{"Rule", "Tokyo"}[i], valueFG, valueBG)
				if focused && row == i {
					assertRoutingColors(t, header[i+1], []string{" · Press Enter to Change", " · Press Enter to Select"}[i], 245, 0)
				}
			}
			assertRoutingColors(t, header[0], "Routing", 63, 0)
		}
	}
	m := newRoutingDetailsModel()
	m.routing.status.State = "pending"
	assertRoutingColors(t, m.routingHeader()[1], "Saved · pending", 245, 0)
}

// assertRoutingColors interprets SGR state at every character in a text span.
// Defaults model the dark terminal in the reference, including reset and reverse.
func assertRoutingColors(t *testing.T, line, span string, wantFG, wantBG int) {
	t.Helper()
	sgr := regexp.MustCompile("\x1b\\[([0-9;]*)m")
	fg, bg, reverse := 7, 0, false
	var plain strings.Builder
	var colors [][2]int
	appendText := func(text string) {
		plain.WriteString(text)
		pair := [2]int{fg, bg}
		if reverse {
			pair = [2]int{bg, fg}
		}
		for range []byte(text) {
			colors = append(colors, pair)
		}
	}
	end := 0
	for _, match := range sgr.FindAllStringSubmatchIndex(line, -1) {
		appendText(line[end:match[0]])
		params := strings.Split(line[match[2]:match[3]], ";")
		for i := 0; i < len(params); i++ {
			n := 0
			if params[i] != "" {
				var err error
				n, err = strconv.Atoi(params[i])
				if err != nil {
					t.Fatal(err)
				}
			}
			switch {
			case n == 0:
				fg, bg, reverse = 7, 0, false
			case n == 7:
				reverse = true
			case n == 27:
				reverse = false
			case n == 39:
				fg = 7
			case n == 49:
				bg = 0
			case n >= 30 && n <= 37:
				fg = n - 30
			case n >= 40 && n <= 47:
				bg = n - 40
			case n >= 90 && n <= 97:
				fg = n - 90 + 8
			case n >= 100 && n <= 107:
				bg = n - 100 + 8
			case n == 38 || n == 48:
				if i+2 >= len(params) || params[i+1] != "5" {
					t.Fatalf("unsupported test color sequence: %q", params)
				}
				color, err := strconv.Atoi(params[i+2])
				if err != nil {
					t.Fatal(err)
				}
				if n == 38 {
					fg = color
				} else {
					bg = color
				}
				i += 2
			}
		}
		end = match[1]
	}
	appendText(line[end:])
	start := strings.Index(plain.String(), span)
	if start < 0 {
		t.Fatalf("missing color span %q in %q", span, plain.String())
	}
	for _, pair := range colors[start : start+len(span)] {
		if pair != [2]int{wantFG, wantBG} {
			t.Fatalf("span %q color=%v; want [%d %d]; ANSI=%q", span, pair, wantFG, wantBG, line)
		}
	}
}
