package connections

import (
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// detailStage keeps the rail outside wrapped field values. A rejected outbound
// uses a broken rail; its destination remains visible as the requested target.
func detailStage(title string, fields []string, width int, rail string, muted bool) string {
	theme := ui.DefaultTheme()
	header := "● " + title
	style := theme.Title
	if muted {
		header, style = "○ "+title, theme.Muted
	}
	lines := []string{style.Render(ansi.Hardwrap(header, width, true))}
	prefix := "  "
	if rail != "" {
		prefix = theme.Muted.Render("│") + " "
	}
	for _, line := range strings.Split(strings.Join(fields, "\n"), "\n") {
		if line == "" {
			lines = append(lines, theme.Muted.Render(rail))
		} else {
			lines = append(lines, prefix+line)
		}
	}
	if rail != "" {
		lines = append(lines, theme.Muted.Render(rail))
	}
	return strings.Join(lines, "\n")
}

// detailSelectionTree reverses only the presentation of mihomo's leaf-first
// chain. Deep levels use explicit numbers once indentation would crowd names.
func detailSelectionTree(chain []string, width int) string {
	if len(chain) == 0 {
		return ui.MissingValue
	}
	var lines []string
	maxIndent := max(0, min(12, width-12))
	for depth := range len(chain) {
		prefix := ""
		if depth > 0 {
			indent := 2 + (depth-1)*3
			if indent+3 > maxIndent {
				// Numbering on its own line preserves room even at the minimum
				// supported width and never splits a name with a depth marker.
				lines = append(lines, fmt.Sprintf("[%d]", depth+1))
				prefix = strings.Repeat(" ", maxIndent)
			} else {
				prefix = strings.Repeat(" ", indent) + "└─ "
			}
		}
		name := value(ui.DisplayProxyName(chain[len(chain)-1-depth]))
		wrapped := strings.Split(ansi.Hardwrap(name, max(1, width-ansi.StringWidth(prefix)), true), "\n")
		for index, line := range wrapped {
			if index == 0 {
				lines = append(lines, prefix+line)
			} else {
				lines = append(lines, strings.Repeat(" ", ansi.StringWidth(prefix))+line)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// geoIPForAddress matches only this stage's address, including equivalent IPv6
// forms and older host:port observations. Lookup causes never enter the view.
func (d *Detail) geoIPForAddress(raw string) string {
	address, err := detailAddress(raw)
	if err != nil {
		return ui.MissingValue
	}
	if !d.geoIPReady {
		return ui.LoadingLabel
	}
	if d.geoIPErr != nil {
		return ui.UnavailableTitle
	}
	for _, record := range d.geoIP {
		candidate, err := detailAddress(record.Address)
		if err != nil || candidate != address {
			continue
		}
		asn := ui.MissingValue
		if record.ASN != 0 {
			asn = fmt.Sprintf("AS%d", record.ASN)
		}
		return value(record.CountryCode) + " · " + asn + "\n" + value(record.Organization)
	}
	return ui.UnavailableTitle
}

func detailAddress(raw string) (netip.Addr, error) {
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	address, err := netip.ParseAddr(strings.Trim(raw, "[]"))
	return address.Unmap(), err
}
