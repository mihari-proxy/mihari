package connections

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type Detail struct {
	connection protocol.Connection
	closed     bool
	tab        int
	scroll     int
	geoIP      []protocol.GeoIPRecord
	geoIPReady bool
	geoIPErr   error
}

func NewDetail(connection protocol.Connection, closed bool) *Detail {
	return &Detail{connection: cloneConnection(connection), closed: closed}
}

func (d *Detail) Update(message tea.Msg) bool {
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return false
	}
	switch key.String() {
	case "esc", "enter":
		return true
	case "left":
		d.tab = max(0, d.tab-1)
		d.scroll = 0
	case "right":
		d.tab = min(2, d.tab+1)
		d.scroll = 0
	case "up":
		d.scroll = max(0, d.scroll-1)
	case "down":
		d.scroll++
	}
	return false
}

func (d *Detail) Refresh(connection protocol.Connection, closed bool) {
	d.connection = cloneConnection(connection)
	d.closed = closed
}

func (d *Detail) SetGeoIP(records []protocol.GeoIPRecord, err error) {
	d.geoIP = append([]protocol.GeoIPRecord(nil), records...)
	d.geoIPReady = true
	d.geoIPErr = err
}

func (d *Detail) View(width, height int) string {
	theme := ui.DefaultTheme()
	tabs := []string{ui.OverviewTabLabel, ui.RawTabLabel, ui.ProxiesTabLabel}
	for index := range tabs {
		if index == d.tab {
			tabs[index] = theme.Title.Render("[" + tabs[index] + "]")
		}
	}
	body := d.overview()
	switch d.tab {
	case 1:
		if raw, err := json.MarshalIndent(d.connection, "", "  "); err == nil {
			body = string(raw)
		}
	case 2:
		lines := make([]string, len(d.connection.Chains))
		for index, hop := range d.connection.Chains {
			lines[index] = strings.Repeat("  ", index) + "\u2192 " + ui.DisplayProxyName(hop)
		}
		body = strings.Join(lines, "\n")
	}
	lines := strings.Split(body, "\n")
	visibleHeight := max(1, height-8)
	// Scroll indicators (▴ / ▾ N more lines) each consume one body row.
	start := min(d.scroll, max(0, len(lines)-visibleHeight))
	end := min(len(lines), start+visibleHeight)
	indicators := 0
	if start > 0 {
		indicators++
	}
	if end < len(lines) {
		indicators++
	}
	if indicators > 0 {
		contentRows := max(1, visibleHeight-indicators)
		start = min(d.scroll, max(0, len(lines)-contentRows))
		end = min(len(lines), start+contentRows)
	}
	bodyLines := make([]string, 0, end-start+2)
	if start > 0 {
		bodyLines = append(bodyLines, theme.Muted.Render("▴"))
	}
	bodyLines = append(bodyLines, lines[start:end]...)
	if end < len(lines) {
		bodyLines = append(bodyLines, theme.Muted.Render(fmt.Sprintf("▾ %d more lines", len(lines)-end)))
	}
	state := ""
	if d.closed {
		state = " - " + ui.ToneStyle(theme, ui.ClassifyStatusTone(ui.ConnectionsClosedLabel)).Render(ui.ConnectionsClosedLabel)
	}
	content := theme.Dialog.Width(min(84, max(36, width-4))).Render(
		theme.Title.Render(ui.ConnectionDetailsTitle+state) + "\n" + strings.Join(tabs, "  ") + "\n\n" + strings.Join(bodyLines, "\n"),
	)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

func (d *Detail) overview() string {
	theme := ui.DefaultTheme()
	connection := d.connection
	chain := ui.DisplayProxyName(strings.Join(connection.Chains, " \u2192 "))
	return fmt.Sprintf("%s\nID  %s\nType  %s / %s\nRule  %s %s\nProcess  %s\nInbound  %s / %s\n\n%s\nSource  %s:%s\nHost  %s\nResolved  %s:%s\nRemote  %s\n\n%s\n%s\n\n%s\n↑%s ↓%s\n\n%s\n%s  %s",
		ui.BasicSectionTitle, value(connection.ID), value(connection.Metadata.Type), ui.StyleNetwork(theme, value(connection.Metadata.Network)),
		value(connection.Rule), value(connection.RulePay), value(connection.Metadata.Process),
		value(connection.Metadata.InboundName), value(connection.Metadata.InboundUser),
		ui.SourceDestinationTitle, value(connection.Metadata.SourceIP), value(connection.Metadata.SourcePort),
		value(connection.Metadata.Host), value(connection.Metadata.DestinationIP), value(connection.Metadata.DestinationPort),
		value(connection.Metadata.RemoteDestination), ui.GeoIPSectionTitle, d.geoIPView(), ui.TrafficSectionTitle,
		ui.FormatRate(connection.UploadSpeed), ui.FormatRate(connection.DownloadSpeed),
		ui.OutboundSectionTitle, ui.ChainLabel, value(chain),
	)
}

func (d *Detail) geoIPView() string {
	if !d.geoIPReady {
		return ui.LoadingLabel
	}
	if d.geoIPErr != nil || len(d.geoIP) == 0 {
		return ui.UnavailableTitle
	}
	lines := make([]string, 0, len(d.geoIP))
	for _, record := range d.geoIP {
		asn := ui.MissingValue
		if record.ASN != 0 {
			asn = fmt.Sprintf("AS%d", record.ASN)
		}
		lines = append(lines, fmt.Sprintf("%s  %s  %s  %s", record.Address, value(record.CountryCode), asn, value(record.Organization)))
	}
	return strings.Join(lines, "\n")
}

func value(input string) string {
	if input == "" {
		return ui.MissingValue
	}
	return input
}
