package connections

import (
	"fmt"
	"net"
	"strings"
	"time"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func (d *Detail) bodyLines(width int) []string {
	theme := ui.DefaultTheme()
	c := d.connection
	meta := c.Metadata
	host := meta.Host
	if host == "" {
		host = meta.DestinationIP
	}
	parts := []string{
		theme.Title.Render(ansi.Hardwrap(detailEndpoint(host, meta.DestinationPort), width, true)),
		theme.Muted.Render(ansi.Wrap(value(meta.Process)+" · "+value(meta.Type)+" · "+value(meta.Network), width, "")),
		"", theme.Title.Render("TRAFFIC"), d.trafficView(width),
		"", theme.Title.Render("ENDPOINTS"),
		detailField("Source", detailEndpoint(meta.SourceIP, meta.SourcePort), width),
		detailField("Destination", detailEndpoint(meta.DestinationIP, meta.DestinationPort), width),
		detailField("Host", value(meta.Host), width),
	}
	optional := func(label, text string) {
		if text != "" {
			parts = append(parts, detailField(label, text, width))
		}
	}
	optional("Sniff host", meta.SniffHost)
	optional("Remote", meta.RemoteDestination)
	parts = append(parts, "", theme.Title.Render("ROUTING"),
		detailField("Rule", value(c.Rule), width),
		detailField("Rule payload", value(c.RulePay), width),
		detailField(ui.ChainLabel, value(ui.DisplayProxyName(strings.Join(c.Chains, " → "))), width),
		detailField(ui.GeoIPSectionTitle, d.geoIPView(), width),
		"", theme.Title.Render("METADATA"),
		detailField("Process", value(meta.Process), width))
	optional("Process path", meta.ProcessPath)
	parts = append(parts, detailField("Inbound name", value(meta.InboundName), width))
	optional("Inbound user", meta.InboundUser)
	parts = append(parts, detailField("Started", detailTime(c.Start), width))
	if !c.ClosedAt.IsZero() {
		parts = append(parts, detailField("Closed observed", detailTime(c.ClosedAt), width))
	}
	parts = append(parts, detailField("Connection ID", value(c.ID), width))
	return strings.Split(strings.Join(parts, "\n"), "\n")
}

func (d *Detail) trafficView(width int) string {
	downLabel, upLabel := "↓ Download", "↑ Upload"
	if d.closed {
		downLabel, upLabel = "Last download rate", "Last upload rate"
	}
	columnWidth := width
	if width >= 64 {
		columnWidth = (width - 4) / 2
	}
	labelWidth := max(16, ansi.StringWidth(downLabel)+2)
	down := detailFieldColumns(downLabel, ui.FormatRate(d.connection.DownloadSpeed), columnWidth, labelWidth) + "\n" +
		detailFieldColumns("Received", ui.FormatBytes(d.connection.Download), columnWidth, labelWidth)
	up := detailFieldColumns(upLabel, ui.FormatRate(d.connection.UploadSpeed), columnWidth, labelWidth) + "\n" +
		detailFieldColumns("Sent", ui.FormatBytes(d.connection.Upload), columnWidth, labelWidth)
	if width >= 64 {
		return lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(columnWidth+4).Render(down), up)
	}
	return down + "\n" + up
}

// detailField wraps values before adding hanging indentation. Hardwrap preserves
// every character in paths and IDs, including spaces and grapheme clusters.
func detailField(label, text string, width int) string {
	return detailFieldColumns(label, text, width, 18)
}

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

func detailEndpoint(host, port string) string {
	if host == "" {
		return ui.MissingValue
	}
	if port == "" {
		return host
	}
	return net.JoinHostPort(host, port)
}

func detailTime(at time.Time) string {
	if at.IsZero() {
		return ui.MissingValue
	}
	return at.Local().Format("2006-01-02 15:04:05")
}

func (d *Detail) geoIPView() string {
	if !d.geoIPReady {
		return ui.LoadingLabel
	}
	if d.geoIPErr != nil || len(d.geoIP) == 0 {
		return ui.UnavailableTitle
	}
	lines := make([]string, 0, len(d.geoIP)*2)
	for _, record := range d.geoIP {
		asn := ui.MissingValue
		if record.ASN != 0 {
			asn = fmt.Sprintf("AS%d", record.ASN)
		}
		lines = append(lines, record.Address+" · "+value(record.CountryCode)+" · "+asn, value(record.Organization))
	}
	return strings.Join(lines, "\n")
}
