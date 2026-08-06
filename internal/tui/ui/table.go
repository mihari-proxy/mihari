package ui

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

// ContentWidthClass is the light responsive tier for table pages.
type ContentWidthClass uint8

const (
	// ContentCompact uses priority fields only (< 90 content columns).
	ContentCompact ContentWidthClass = iota
	// ContentFull shows the expanded field set.
	ContentFull
)

// CompactContentWidth is the threshold for light responsive tables (design C-lite).
const CompactContentWidth = 90

// Align is horizontal cell alignment.
type Align uint8

const (
	AlignLeft Align = iota
	AlignRight
)

// TableColumn describes a flex-capable column for FitColumnWidths.
type TableColumn struct {
	ID       string
	Title    string
	MinWidth int
	MaxWidth int // 0 means unlimited
	Flex     int // 0 means fixed at MinWidth (clamped by MaxWidth)
	Align    Align
	// Priority is the drop order when width runs out: larger survives longer.
	// 0 means never dropped (always kept; the zero value is the safe default
	// that degrades to plain FitColumnWidths shrinking).
	Priority int
}

// ClassifyContentWidth returns Compact vs Full for the given content pane width.
func ClassifyContentWidth(width int) ContentWidthClass {
	if width < CompactContentWidth {
		return ContentCompact
	}
	return ContentFull
}

// FitColumnWidths distributes total width across columns.
// gap is the number of spaces between adjacent columns (applied len(cols)-1 times).
func FitColumnWidths(cols []TableColumn, total, gap int) []int {
	n := len(cols)
	if n == 0 {
		return nil
	}
	if gap < 0 {
		gap = 0
	}
	widths := make([]int, n)
	usedGaps := gap * max(0, n-1)
	budget := max(0, total-usedGaps)

	flexTotal := 0
	fixedSum := 0
	for i, col := range cols {
		minW := max(1, col.MinWidth)
		if col.Flex <= 0 {
			w := minW
			if col.MaxWidth > 0 {
				w = min(w, col.MaxWidth)
			}
			widths[i] = w
			fixedSum += w
			continue
		}
		widths[i] = minW
		flexTotal += col.Flex
	}
	if flexTotal == 0 {
		return widths
	}
	// fixedSum above included only Flex==0; total assigned so far = fixedSum + flexMinSum.
	flexMinSum := 0
	for i, col := range cols {
		if col.Flex > 0 {
			flexMinSum += widths[i]
		}
	}
	remain := budget - fixedSum - flexMinSum
	if remain < 0 {
		// Shrink flex columns proportionally toward 1 if over budget.
		need := fixedSum + flexMinSum - budget
		for need > 0 {
			progress := false
			for i, col := range cols {
				if col.Flex <= 0 || widths[i] <= 1 {
					continue
				}
				widths[i]--
				need--
				progress = true
				if need == 0 {
					break
				}
			}
			if !progress {
				break
			}
		}
		return widths
	}
	// Distribute remain by flex weight.
	given := 0
	for i, col := range cols {
		if col.Flex <= 0 {
			continue
		}
		extra := remain * col.Flex / flexTotal
		widths[i] += extra
		given += extra
		if col.MaxWidth > 0 && widths[i] > col.MaxWidth {
			given -= widths[i] - col.MaxWidth
			widths[i] = col.MaxWidth
		}
	}
	// Hand leftover to the last flex column.
	leftover := remain - given
	if leftover != 0 {
		for i := n - 1; i >= 0; i-- {
			if cols[i].Flex <= 0 {
				continue
			}
			widths[i] += leftover
			if cols[i].MaxWidth > 0 && widths[i] > cols[i].MaxWidth {
				widths[i] = cols[i].MaxWidth
			}
			break
		}
	}
	return widths
}

// TruncateVisible shortens s to at most width terminal columns, appending "…".
func TruncateVisible(s string, width int) string {
	return truncateStyled(s, width)
}

// truncateStyled shortens a possibly styled string to at most width terminal
// columns, appending "…". ANSI escape sequences (\x1b[…m) carry no width and
// are preserved verbatim as style prefixes for the runes they precede; a
// trailing unterminated sequence is kept so the SGR state closes at the cut
// point instead of leaking into following cells.
func truncateStyled(text string, width int) string {
	if width <= 0 {
		return ""
	}
	type cell struct {
		prefix string
		r      rune
	}
	cells := make([]cell, 0, len(text))
	var escape strings.Builder
	inEscape := false
	for _, r := range text {
		switch {
		case r == '\x1b':
			inEscape = true
			escape.WriteRune(r)
		case inEscape:
			escape.WriteRune(r)
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
		default:
			cells = append(cells, cell{prefix: escape.String(), r: r})
			escape.Reset()
		}
	}
	trailing := escape.String()
	total := 0
	for _, c := range cells {
		total += runeWidth(c.r)
	}
	if total <= width {
		return text
	}
	if width == 1 {
		return "…" + trailing
	}
	// Walk visible runes until width-1 columns, then ellipsis.
	used := 0
	limit := width - 1
	var out strings.Builder
	for _, c := range cells {
		rw := runeWidth(c.r)
		if used+rw > limit {
			break
		}
		out.WriteString(c.prefix)
		out.WriteRune(c.r)
		used += rw
	}
	out.WriteString("…")
	out.WriteString(trailing)
	return out.String()
}

func runeWidth(r rune) int {
	// lipgloss/Width uses unicode width; approximate with utf8 display via lipgloss.
	return lipgloss.Width(string(r))
}

// PadCell pads or truncates s to exactly width visible columns.
func PadCell(s string, width int, align Align) string {
	if width <= 0 {
		return ""
	}
	s = TruncateVisible(s, width)
	pad := width - lipgloss.Width(s)
	if pad <= 0 {
		return s
	}
	spaces := strings.Repeat(" ", pad)
	if align == AlignRight {
		return spaces + s
	}
	return s + spaces
}

// JoinCells joins already-padded cells with gap spaces.
func JoinCells(cells []string, gap int) string {
	if gap < 0 {
		gap = 0
	}
	return strings.Join(cells, strings.Repeat(" ", gap))
}

// RenderHeaderRow builds a TableHeader-styled header and a muted rule line.
// Titles and alignment come from the column definitions; focusedIndex < 0
// means no focused column. Returns (headerLine, ruleLine).
func RenderHeaderRow(theme Theme, cols []TableColumn, widths []int, gap, focusedIndex int, contentFocused bool) (string, string) {
	n := min(len(cols), len(widths))
	cells := make([]string, n)
	total := gap * max(0, n-1)
	for i := 0; i < n; i++ {
		padded := PadCell(cols[i].Title, widths[i], cols[i].Align)
		if contentFocused && focusedIndex == i {
			cells[i] = theme.ControlActive.UnsetPadding().Render(padded)
		} else {
			cells[i] = theme.TableHeader.Render(padded)
		}
		total += widths[i]
	}
	header := JoinCells(cells, gap)
	rule := theme.SurfaceBorder.Render(strings.Repeat("─", max(1, lipgloss.Width(header))))
	return header, rule
}

// StyleLogLevel colors a log level label (returns uppercase display text styled).
func StyleLogLevel(theme Theme, level string) string {
	label := strings.ToUpper(strings.TrimSpace(level))
	if label == "" {
		label = MissingValue
	}
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error", "err", "fatal", "panic":
		return theme.Danger.Render(label)
	case "warning", "warn":
		return theme.Warning.Render(label)
	case "info", "information":
		return theme.Info.Render(label)
	case "debug", "trace":
		return theme.Muted.Render(label)
	default:
		return theme.Muted.Render(label)
	}
}

// StyleRuleType colors a Clash-style rule type token. Rule TYPE is a category,
// not a status: it stays one Info color so the column reads as a single band
// (DOMAIN/IP/MATCH/PROCESS no longer get four divergent colors).
func StyleRuleType(theme Theme, ruleType string) string {
	label := strings.TrimSpace(ruleType)
	if label == "" {
		return theme.Muted.Render(MissingValue)
	}
	return theme.Info.Render(label)
}

// StyleProviderStatus colors rule-provider status text through the shared tone
// classifier so the whole shell agrees on what each status word means.
func StyleProviderStatus(theme Theme, status string) string {
	label := strings.TrimSpace(status)
	if label == "" {
		return theme.Muted.Render(MissingValue)
	}
	return ToneStyle(theme, ClassifyStatusTone(label)).Render(label)
}

// StyleProxyTarget colors rule/proxy targets through the shared tone classifier
// (DIRECT→Neutral, REJECT/BLOCK→Negative, PASS/COMPATIBLE→Caution, default
// proxy→Positive). Behavior is unchanged; only the dispatch is unified.
func StyleProxyTarget(theme Theme, target string) string {
	label := strings.TrimSpace(target)
	if label == "" {
		return theme.Muted.Render(MissingValue)
	}
	tone := TonePositive
	switch strings.ToUpper(label) {
	case "DIRECT":
		tone = ToneNeutral
	case "REJECT", "REJECT-DROP", "BLOCK":
		tone = ToneNegative
	case "PASS", "COMPATIBLE":
		tone = ToneCaution
	}
	return ToneStyle(theme, tone).Render(label)
}

// StyleTrafficPair formats colored UL/DL rate pair for connection primary lines.
func StyleTrafficPair(theme Theme, upLabel, downLabel string) string {
	up := theme.Success.Render(upLabel)
	down := theme.Info.Render(downLabel)
	return up + "  " + down
}

// splitRate splits a formatted rate ("1.2 MiB/s") at its last space into the
// number part ("1.2") and the unit part ("MiB/s").
func splitRate(rate string) (digits, unit string) {
	if index := strings.LastIndexByte(rate, ' '); index >= 0 {
		return rate[:index], rate[index+1:]
	}
	return rate, ""
}

// RenderTrafficSlot renders marker+rate (e.g. "↑1.2 MiB/s") into a w-wide
// slot: the unit is right-anchored at the slot's right edge and the digits
// stretch between marker and unit. When the slot is too narrow the digits are
// truncated first — the unit is never cut.
func RenderTrafficSlot(style lipgloss.Style, marker, rate string, w int) string {
	if w <= 0 {
		return ""
	}
	digits, unit := splitRate(rate)
	reserve := lipgloss.Width(unit) + 1 // space before the anchored unit
	bodyW := max(0, w-reserve)
	body := TruncateVisible(marker+digits, bodyW)
	if bodyW > 0 {
		body = strings.Repeat(" ", bodyW-lipgloss.Width(body)) + body
	}
	slot := body + " " + unit
	if bodyW == 0 && lipgloss.Width(slot) > w {
		// Narrower than unit+space: fall back to truncating the unit itself.
		slot = TruncateVisible(marker+unit, w)
	}
	return style.Render(slot)
}

// RenderTrafficColumn combines the ↑/↓ slots (two spaces apart) into the
// Conns traffic column cell of width w, keeping both units vertically aligned.
func RenderTrafficColumn(theme Theme, upRate, downRate string, w int) string {
	slot := max(1, (w-2)/2)
	up := RenderTrafficSlot(theme.Success, "↑", upRate, slot)
	down := RenderTrafficSlot(theme.Info, "↓", downRate, w-slot-2)
	return up + "  " + down
}

// StyleNetwork colors network/protocol tokens like TCP/UDP.
func StyleNetwork(theme Theme, network string) string {
	label := strings.TrimSpace(network)
	if label == "" {
		return theme.Muted.Render(MissingValue)
	}
	upper := strings.ToUpper(label)
	switch {
	case strings.Contains(upper, "UDP"):
		return theme.Warning.Render(label)
	case strings.Contains(upper, "TCP"):
		return theme.Info.Render(label)
	default:
		return theme.Muted.Render(label)
	}
}

// VisibleWidth is lipgloss.Width for call sites that prefer the name.
func VisibleWidth(s string) int { return lipgloss.Width(s) }

// FocusPrefix returns the focus marker or two spaces.
func FocusPrefix(focused bool) string {
	if focused {
		return FocusMarker
	}
	return "  "
}
