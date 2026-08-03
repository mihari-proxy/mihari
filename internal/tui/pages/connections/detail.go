package connections

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

type Detail struct {
	connection protocol.Connection
	closed     bool
	tab        int
	scroll     int
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
	case "esc":
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

func (d *Detail) View(width, height int) string {
	theme := ui.DefaultTheme()
	tabs := []string{ui.OverviewTabLabel, ui.RawTabLabel, ui.ProxiesTabLabel}
	for index := range tabs {
		if index == d.tab {
			tabs[index] = theme.Title.Render("[" + tabs[index] + "]")
		}
	}
	body := d.overview()
	if d.tab == 1 {
		if raw, err := json.MarshalIndent(d.connection, "", "  "); err == nil {
			body = string(raw)
		}
	} else if d.tab == 2 {
		lines := make([]string, len(d.connection.Chains))
		for index, hop := range d.connection.Chains {
			lines[index] = strings.Repeat("  ", index) + "\u2192 " + hop
		}
		body = strings.Join(lines, "\n")
	}
	lines := strings.Split(body, "\n")
	visibleHeight := max(1, height-8)
	start := min(d.scroll, max(0, len(lines)-visibleHeight))
	end := min(len(lines), start+visibleHeight)
	state := ""
	if d.closed {
		state = " - " + ui.ConnectionsClosedLabel
	}
	content := theme.Dialog.Width(min(84, max(36, width-4))).Render(
		theme.Title.Render(ui.ConnectionDetailsTitle+state) + "\n" + strings.Join(tabs, "  ") + "\n\n" + strings.Join(lines[start:end], "\n"),
	)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

func (d *Detail) overview() string {
	connection := d.connection
	chain := strings.Join(connection.Chains, " \u2192 ")
	return fmt.Sprintf("%s\nID  %s\nType  %s / %s\nRule  %s %s\nProcess  %s\nInbound  %s / %s\n\n%s\nSource  %s:%s\nHost  %s\nResolved  %s:%s\nRemote  %s\n\n%s\nUL  %d B - %d B/s\nDL  %d B - %d B/s\n\n%s\n%s  %s",
		ui.BasicSectionTitle, value(connection.ID), value(connection.Metadata.Type), value(connection.Metadata.Network),
		value(connection.Rule), value(connection.RulePay), value(connection.Metadata.Process),
		value(connection.Metadata.InboundName), value(connection.Metadata.InboundUser),
		ui.SourceDestinationTitle, value(connection.Metadata.SourceIP), value(connection.Metadata.SourcePort),
		value(connection.Metadata.Host), value(connection.Metadata.DestinationIP), value(connection.Metadata.DestinationPort),
		value(connection.Metadata.RemoteDestination), ui.TrafficSectionTitle,
		connection.Upload, connection.UploadSpeed, connection.Download, connection.DownloadSpeed,
		ui.OutboundSectionTitle, ui.ChainLabel, value(chain),
	)
}

func value(input string) string {
	if input == "" {
		return ui.MissingValue
	}
	return input
}
