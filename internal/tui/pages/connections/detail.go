package connections

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// Detail presents one observed connection without changing its runtime state.
type Detail struct {
	connection protocol.Connection
	closed     bool
	paused     bool
	scroll     int
	width      int
	height     int
	geoIP      []protocol.GeoIPRecord
	geoIPReady bool
	geoIPErr   error
}

// NewDetail retains a copy of the connection for the detail view.
func NewDetail(connection protocol.Connection, closed bool) *Detail {
	return &Detail{connection: cloneConnection(connection), closed: closed}
}

// Update handles local scrolling and reports when the user closes the detail.
func (d *Detail) Update(message tea.Msg) bool {
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return false
	}
	d.clampScroll()
	switch key.String() {
	case "esc", "enter":
		return true
	case "up":
		d.scroll--
	case "down":
		d.scroll++
	}
	d.clampScroll()
	return false
}

// SetSize updates the viewport and keeps the stored offset within its bounds.
func (d *Detail) SetSize(width, height int) {
	d.width, d.height = width, height
	d.clampScroll()
}

// Refresh replaces the observation while preserving a valid scroll position.
func (d *Detail) Refresh(connection protocol.Connection, closed bool) {
	d.connection = cloneConnection(connection)
	d.closed = closed
	d.clampScroll()
}

// SetGeoIP attaches the result for this connection's public destination addresses.
func (d *Detail) SetGeoIP(records []protocol.GeoIPRecord, err error) {
	d.geoIP = append([]protocol.GeoIPRecord(nil), records...)
	d.geoIPReady = true
	d.geoIPErr = err
	d.clampScroll()
}

type detailLayout struct {
	outer  int
	header string
	lines  []string
	rows   int
}

// layout budgets fixed chrome and the scrollable body in terminal cells.
func (d *Detail) layout() detailLayout {
	outer := min(88, d.width-2)
	if outer < 16 || d.height < 7 {
		return detailLayout{}
	}
	theme := ui.DefaultTheme()
	state := theme.Success.Render("● " + ui.ConnectionsActiveLabel)
	if d.closed {
		state = theme.Muted.Render("○ " + ui.ConnectionsClosedLabel)
	}
	if d.paused {
		state += theme.Warning.Render(" · Paused")
	}
	textWidth := outer - 4 // Two border cells and one padding cell on each side.
	header := ansi.Wrap(state, textWidth, "")
	lines := d.bodyLines(textWidth)
	rows := max(1, d.height-2-lipgloss.Height(header)-1) // Border, status, blank row.
	if len(lines) > rows {
		rows-- // Reserve a stable position row only when scrolling is needed.
	}
	return detailLayout{outer: outer, header: header, lines: lines, rows: rows}
}

// clampScroll removes excess offset after navigation, resize or content changes.
func (d *Detail) clampScroll() {
	layout := d.layout()
	d.scroll = min(max(0, d.scroll), max(0, len(layout.lines)-layout.rows))
}

// View renders a bounded, centered single-page connection detail.
func (d *Detail) View(width, height int) string {
	d.SetSize(width, height)
	if width <= 0 || height <= 0 {
		return ""
	}
	theme := ui.DefaultTheme()
	layout := d.layout()
	if layout.rows == 0 {
		return theme.Muted.Render(ansi.Truncate("Resize terminal · Enter/Esc close", width, ""))
	}
	end := min(len(layout.lines), d.scroll+layout.rows)
	body := layout.header + "\n\n" + strings.Join(layout.lines[d.scroll:end], "\n")
	if len(layout.lines) > layout.rows {
		position := fmt.Sprintf("%d–%d / %d", d.scroll+1, end, len(layout.lines))
		body += "\n" + theme.Muted.Render(ansi.Truncate(position, layout.outer-4, ""))
	}
	panel := ui.RenderBorderedSection(theme, ui.ConnectionDetailsTitle, body, layout.outer-2)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

// value uses the shared missing-value marker without changing nonempty text.
func value(input string) string {
	if input == "" {
		return ui.MissingValue
	}
	return input
}
