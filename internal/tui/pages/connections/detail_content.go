package connections

import (
	"net"
	"strings"
	"time"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// bodyLines attaches observations to their route stages before viewport slicing.
func (d *Detail) bodyLines(width int) []string {
	width = max(1, width)
	theme := ui.DefaultTheme()
	c := d.connection
	meta := c.Metadata
	host := meta.Host
	if host == "" {
		host = meta.DestinationIP
	}
	inner := max(1, width-2)
	app := []string{ansi.Hardwrap(value(meta.Process), inner, true),
		detailField("Source", detailEndpoint(meta.SourceIP, meta.SourcePort), inner)}
	optional := func(fields *[]string, label, text string) {
		if text != "" {
			*fields = append(*fields, detailField(label, text, inner))
		}
	}
	optional(&app, "Process path", meta.ProcessPath)
	inbound := strings.ToUpper(value(meta.Type)) + " · " + strings.ToUpper(value(meta.Network))
	if meta.InboundName != "" {
		inbound = meta.InboundName + " · " + inbound
	}
	rule := value(c.Rule)
	if c.RulePay != "" {
		rule += " · " + c.RulePay
	}
	routing := []string{detailField("Inbound", inbound, inner)}
	optional(&routing, "Inbound user", meta.InboundUser)
	routing = append(routing, detailField("Rule Matched", rule, inner), "", detailSelectionTree(c.Chains, inner))

	outbound := ui.MissingValue
	if len(c.Chains) > 0 {
		outbound = value(ui.DisplayProxyName(c.Chains[0]))
	}
	rejected := outbound == "REJECT" || outbound == "REJECT-DROP"
	outbound = ansi.Hardwrap(outbound, inner, true)
	if rejected {
		outbound = theme.Danger.Render(outbound)
	}
	out := []string{outbound,
		detailField("Remote", value(meta.RemoteDestination), inner),
		detailField("GeoIP", d.geoIPForAddress(meta.RemoteDestination), inner)}
	dest := []string{detailField("Host", value(meta.Host), inner),
		detailField("Destination", detailEndpoint(meta.DestinationIP, meta.DestinationPort), inner)}
	optional(&dest, "Sniff host", meta.SniffHost)
	dest = append(dest, detailField("GeoIP", d.geoIPForAddress(meta.DestinationIP), inner))
	link := "│"
	if rejected {
		link = "┆"
	}
	parts := []string{
		theme.Title.Render(ansi.Hardwrap(detailEndpoint(host, meta.DestinationPort), width, true)),
		"", d.trafficView(width), "",
		detailStage("APPLICATION", app, width, "│", false),
		detailStage("ROUTING", routing, width, "│", false),
		detailStage("OUTBOUND", out, width, link, false),
		detailStage("DESTINATION", dest, width, "", rejected), "",
	}
	parts = append(parts, detailField("Started", detailTime(c.Start), width))
	if !c.ClosedAt.IsZero() {
		parts = append(parts, detailField("Closed observed", detailTime(c.ClosedAt), width))
	}
	parts = append(parts, detailField("Connection ID", value(c.ID), width))
	return strings.Split(strings.Join(parts, "\n"), "\n")
}

// trafficView stacks narrow layouts and distinguishes final from live rates.
func (d *Detail) trafficView(width int) string {
	theme := ui.DefaultTheme()
	rateLabel := "Rate"
	if d.closed {
		rateLabel = "Last rate"
	}
	columnWidth := width
	if width >= 64 {
		columnWidth = (width - 4) / 2
	}
	column := func(title, totalLabel, rate, total string, style lipgloss.Style) string {
		return style.Render(ansi.Hardwrap(title, columnWidth, true)) + "\n" +
			detailFieldColumns(rateLabel, style.Render(rate), columnWidth, 12) + "\n" +
			detailFieldColumns(totalLabel, style.Render(total), columnWidth, 12)
	}
	up := column("↑ Upload", "Sent", ui.FormatRate(d.connection.UploadSpeed), ui.FormatBytes(d.connection.Upload), theme.Success)
	down := column("↓ Download", "Received", ui.FormatRate(d.connection.DownloadSpeed), ui.FormatBytes(d.connection.Download), theme.Info)
	if width >= 64 {
		return lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(columnWidth+4).Render(up), down)
	}
	return up + "\n" + down
}

// detailField wraps values before adding hanging indentation. Hardwrap preserves
// every character in paths and IDs, including spaces and grapheme clusters.
func detailField(label, text string, width int) string {
	return detailFieldColumns(label, text, width, 18)
}

// detailFieldColumns keeps continuation lines aligned with the value column.
func detailFieldColumns(label, text string, width, labelWidth int) string {
	theme := ui.DefaultTheme()
	labelWidth = max(labelWidth, ansi.StringWidth(label)+2)
	if width-labelWidth < 12 {
		return theme.Muted.Render(ansi.Hardwrap(label, width, true)) + "\n" + ansi.Hardwrap(value(text), width, true)
	}
	lines := strings.Split(ansi.Hardwrap(value(text), width-labelWidth, true), "\n")
	for i := range lines {
		prefix := strings.Repeat(" ", labelWidth)
		if i == 0 {
			prefix = theme.Muted.Render(label) + strings.Repeat(" ", labelWidth-ansi.StringWidth(label))
		}
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

// detailEndpoint brackets IPv6 addresses and avoids inventing missing ports.
func detailEndpoint(host, port string) string {
	if host == "" {
		return ui.MissingValue
	}
	if port == "" {
		return host
	}
	return net.JoinHostPort(host, port)
}

// detailTime renders an observation in local time while preserving missing values.
func detailTime(at time.Time) string {
	if at.IsZero() {
		return ui.MissingValue
	}
	return at.Local().Format("2006-01-02 15:04:05")
}
