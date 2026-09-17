package connections

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestDetail_VerticalRouteOwnsConnectionFields(t *testing.T) {
	c := detailFixture()
	c.Chains = []string{"[US] Haruka 0x", "Kanata.IM", "Microsoft"}
	c.Metadata.Type = "HTTP"
	c.Metadata.RemoteDestination = "203.0.113.25"
	d := NewDetail(c, false)
	d.SetGeoIP([]protocol.GeoIPRecord{
		{Address: "203.0.113.25", CountryCode: "AU", ASN: 64501, Organization: "Peer Network"},
		{Address: "203.0.113.24", CountryCode: "JP", ASN: 64500, Organization: "Target Network"},
	}, nil)
	body := stripConnANSI(strings.Join(d.bodyLines(84), "\n"))
	previous := -1
	for _, title := range []string{"● APPLICATION", "● ROUTING", "● OUTBOUND", "● DESTINATION", "Started", "Connection ID"} {
		index := strings.Index(body, title)
		if index <= previous {
			t.Fatalf("missing or misplaced %q:\n%s", title, body)
		}
		previous = index
	}
	for _, obsolete := range []string{"● INBOUND", "CONNECTION INFO", "METADATA", "Inbound name", "Rule payload"} {
		if strings.Contains(body, obsolete) {
			t.Errorf("obsolete section or field %q", obsolete)
		}
	}
	app := routeSection(t, body, "● APPLICATION", "● ROUTING")
	if !strings.Contains(app, c.Metadata.ProcessPath) || !strings.Contains(app, "[2001:db8::1]:52341") {
		t.Fatalf("application must own source and full process path:\n%s", app)
	}
	routing := routeSection(t, body, "● ROUTING", "● OUTBOUND")
	for _, want := range []string{"mixed-in · HTTP · TCP", "test-user", "Rule Matched", "DomainSuffix · example.test",
		"│ Microsoft\n│   └─ Kanata.IM\n│      └─ [US] Haruka 0x"} {
		if !strings.Contains(routing, want) {
			t.Errorf("routing missing %q:\n%s", want, routing)
		}
	}
	outbound := routeSection(t, body, "● OUTBOUND", "● DESTINATION")
	for _, want := range []string{"[US] Haruka 0x", c.Metadata.RemoteDestination, "AU · AS64501", "Peer Network"} {
		if !strings.Contains(outbound, want) {
			t.Errorf("outbound missing %q:\n%s", want, outbound)
		}
	}
	destination := routeSection(t, body, "● DESTINATION", "Started")
	for _, want := range []string{c.Metadata.Host, "203.0.113.24:443", c.Metadata.SniffHost, "JP · AS64500", "Target Network"} {
		if !strings.Contains(destination, want) {
			t.Errorf("destination missing %q:\n%s", want, destination)
		}
	}
	if strings.Contains(outbound, "Target Network") || strings.Contains(destination, "Peer Network") {
		t.Fatal("GeoIP result attached to the wrong endpoint")
	}
	if !slices.Equal(c.Chains, d.connection.Chains) {
		t.Fatal("rendering changed the observed chain order")
	}
}

func routeSection(t *testing.T, body, start, end string) string {
	t.Helper()
	a, b := strings.Index(body, start), strings.Index(body, end)
	if a < 0 || b <= a {
		t.Fatalf("missing section %q before %q:\n%s", start, end, body)
	}
	return body[a:b]
}

func TestDetail_RouteWithMissingOrSingleOutbound(t *testing.T) {
	for _, name := range []string{"", "DIRECT", "REJECT", "REJECT-DROP"} {
		t.Run(name, func(t *testing.T) {
			c := protocol.Connection{Rule: "Match"}
			if name != "" {
				c.Chains = []string{name}
			}
			raw := strings.Join(NewDetail(c, false).bodyLines(84), "\n")
			body := stripConnANSI(raw)
			target := "● DESTINATION"
			if strings.HasPrefix(name, "REJECT") {
				target = "○ DESTINATION"
				if !strings.Contains(raw, ui.DefaultTheme().Danger.Render(name)) || !strings.Contains(body, "┆\n") ||
					!strings.Contains(raw, ui.DefaultTheme().Muted.Render(target)) {
					t.Fatal("rejection must mark the outbound and break the route before a muted destination")
				}
			} else if strings.Contains(body, "┆") {
				t.Fatal("ordinary route was marked rejected")
			}
			route := routeSection(t, body, "● ROUTING", target)
			want := name
			if want == "" {
				want = "—"
			}
			if !strings.Contains(route, want) || strings.Contains(route, "└─") || strings.Contains(route, "Match ·") {
				t.Fatalf("single or missing outbound invented a hop or payload:\n%s", route)
			}
			if !strings.Contains(route, "Remote") || strings.Contains(route, ":443") {
				t.Fatal("missing remote must be retained without an invented address")
			}
		})
	}
}

func TestDetail_DeepRoutingTreeWrapsWithoutLosingNames(t *testing.T) {
	c := detailFixture()
	c.Chains = nil
	for i := range 18 {
		c.Chains = append(c.Chains, fmt.Sprintf("node-%02d-日本-with-a-long-name", i))
	}
	for _, width := range []int{12, 30, 60, 84} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			lines := NewDetail(c, false).bodyLines(width)
			var flat strings.Builder
			for _, line := range lines {
				if ansi.StringWidth(line) > width {
					t.Fatalf("line exceeds %d columns: %q", width, line)
				}
				flat.WriteString(strings.TrimLeft(stripConnANSI(line), " │└─●"))
			}
			for _, name := range c.Chains {
				if !strings.Contains(flat.String(), name) {
					t.Errorf("tree lost %q", name)
				}
			}
			if !strings.Contains(flat.String(), "[18]") {
				t.Error("capped indentation must retain the original level number")
			}
		})
	}
}

func TestDetail_TrafficColorsRatesAndTotals(t *testing.T) {
	theme := ui.DefaultTheme()
	for _, closed := range []bool{false, true} {
		for _, width := range []int{30, 84} {
			t.Run(fmt.Sprintf("closed=%v/width=%d", closed, width), func(t *testing.T) {
				d := NewDetail(detailFixture(), closed)
				view := d.trafficView(width)
				for _, want := range []string{
					theme.Success.Render("1.0 KiB/s"), theme.Success.Render("2.0 KiB"),
					theme.Info.Render("3.0 KiB/s"), theme.Info.Render("4.0 KiB"),
				} {
					if !strings.Contains(view, want) {
						t.Errorf("missing directional color %q in %q", want, view)
					}
				}
				plain := stripConnANSI(view)
				if !strings.Contains(plain, "↑ Upload") || strings.Index(plain, "↑ Upload") > strings.Index(plain, "↓ Download") {
					t.Error("upload must precede download")
				}
				if closed && !strings.Contains(plain, "Last rate") {
					t.Error("closed connection claims a current rate")
				}
			})
		}
	}
}

func TestDetail_DirectKeepsBothEndpointAssociations(t *testing.T) {
	c := detailFixture()
	c.Chains = []string{"DIRECT", "Local"}
	c.Metadata.DestinationIP, c.Metadata.RemoteDestination = "2001:db8::24", "2001:0db8:0:0::24"
	d := NewDetail(c, false)
	d.SetGeoIP([]protocol.GeoIPRecord{{Address: "2001:db8::24", CountryCode: "JP", Organization: "Shared endpoint"}}, nil)
	body := stripConnANSI(strings.Join(d.bodyLines(84), "\n"))
	if strings.Count(body, "Shared endpoint") != 2 {
		t.Fatal("equivalent IPs must retain GeoIP in both logical stages")
	}
	c.Metadata.DestinationIP = ""
	d.Refresh(c, false)
	body = stripConnANSI(strings.Join(d.bodyLines(84), "\n"))
	dest := routeSection(t, body, "● DESTINATION", "Started")
	if strings.Contains(dest, "2001:") || strings.Contains(dest, "Shared endpoint") || !strings.Contains(dest, c.Metadata.Host) {
		t.Fatal("missing destination IP was replaced with the direct remote peer")
	}
}
