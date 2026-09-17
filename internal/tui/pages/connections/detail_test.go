package connections

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// TestDetail_SinglePage verifies that neither the view nor arrow keys expose tabs.
func TestDetail_SinglePage(t *testing.T) {
	d := NewDetail(protocol.Connection{ID: "detail-test", Chains: []string{"Group", "Node"}}, false)
	view := stripConnANSI(d.View(100, 40))
	for _, removed := range []string{"Overview", "Raw", "Proxies", `"metadata":`} {
		if strings.Contains(view, removed) {
			t.Errorf("obsolete detail content %q", removed)
		}
	}
	d.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	before := d.View(100, 16)
	for _, key := range []rune{tea.KeyRight, tea.KeyLeft} {
		if d.Update(tea.KeyPressMsg{Code: key}) || d.View(100, 16) != before {
			t.Error("left/right changed single-page details")
		}
	}
}

// detailFixture supplies deterministic, synthetic connection metadata and counters.
func detailFixture() protocol.Connection {
	return protocol.Connection{
		ID: "8d37b6a2-51a4-4f9e-b1d9-6e84b12fa205", Start: time.Date(2026, 9, 16, 10, 30, 0, 0, time.Local),
		Upload: 2048, Download: 4096, UploadSpeed: 1024, DownloadSpeed: 3072,
		Chains: []string{"Japan 01", "Auto Select", "Proxy"}, Rule: "DomainSuffix", RulePay: "example.test",
		Metadata: protocol.ConnectionMetadata{
			Host: "api.example.test", Network: "TCP", Type: "HTTP", SourceIP: "2001:db8::1", SourcePort: "52341",
			DestinationIP: "203.0.113.24", DestinationPort: "443", SniffHost: "sniff.example.test",
			RemoteDestination: "203.0.113.25", Process: "chrome.exe", ProcessPath: "C:/Apps/chrome.exe",
			InboundName: "mixed-in", InboundUser: "test-user",
		},
	}
}

// TestDetail_FieldsAndStates verifies complete observations in active and closed views.
func TestDetail_FieldsAndStates(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(fmt.Sprintf("closed=%v", closed), func(t *testing.T) {
			c := detailFixture()
			if closed {
				c.ClosedAt = c.Start.Add(time.Minute)
			}
			d := NewDetail(c, closed)
			d.SetGeoIP([]protocol.GeoIPRecord{
				{Address: "203.0.113.24", CountryCode: "JP", ASN: 64500, Organization: "Example Network"},
				{Address: "203.0.113.25", CountryCode: "AU", ASN: 64501, Organization: "Other Network"},
			}, nil)
			view := stripConnANSI(d.View(100, 100))
			for _, want := range []string{c.ID, "[2001:db8::1]:52341", "203.0.113.24:443", "api.example.test:443",
				"sniff.example.test", c.Metadata.ProcessPath, "mixed-in", "test-user", "2026-09-16 10:30:00",
				"Proxy", "Auto Select", "Japan 01", "DomainSuffix", "example.test", "Received", "Sent", "2.0 KiB", "4.0 KiB",
				"203.0.113.24", "JP", "AS64500", "Example Network", "203.0.113.25", "AU", "AS64501", "Other Network"} {
				if !strings.Contains(view, want) {
					t.Errorf("missing %q in:\n%s", want, view)
				}
			}
			if closed {
				for _, want := range []string{"Closed", "Last rate", "↑ Upload", "↓ Download", "Closed observed", "2026-09-16 10:31:00"} {
					if !strings.Contains(view, want) {
						t.Errorf("closed detail missing %q", want)
					}
				}
			} else if !strings.Contains(view, "Active") || strings.Contains(view, "Closed observed") {
				t.Error("incorrect active status")
			}
			if d.connection.UploadSpeed != c.UploadSpeed || d.connection.DownloadSpeed != c.DownloadSpeed {
				t.Fatal("render changed observed rates")
			}
		})
	}
}

// TestDetail_MissingFieldsAndGeoIP checks safe presentation of unavailable data.
func TestDetail_MissingFieldsAndGeoIP(t *testing.T) {
	d := NewDetail(protocol.Connection{Metadata: protocol.ConnectionMetadata{DestinationIP: "203.0.113.24"}}, false)
	view := stripConnANSI(d.View(100, 100))
	if !strings.Contains(view, "Loading") {
		t.Error("missing GeoIP loading state")
	}
	for _, omitted := range []string{"Sniff host", "Process path", "Inbound user", "—:—", "0001-01-01"} {
		if strings.Contains(view, omitted) {
			t.Errorf("empty field shown: %q", omitted)
		}
	}
	d.SetGeoIP(nil, errors.New("synthetic-sensitive-cause"))
	view = stripConnANSI(d.View(100, 100))
	if !strings.Contains(view, "Unavailable") || strings.Contains(view, "synthetic-sensitive-cause") {
		t.Fatal("GeoIP failure must show only safe unavailable state")
	}
}

// TestDetail_LayoutBounds checks viewport containment across supported sizes.
func TestDetail_LayoutBounds(t *testing.T) {
	c := longDetailFixture()
	for _, size := range [][2]int{{100, 32}, {80, 24}, {60, 16}, {36, 12}, {20, 6}, {12, 8}, {1, 1}, {0, 0}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			d := NewDetail(c, false)
			for range 3 {
				view := d.View(size[0], size[1])
				if lipgloss.Width(view) > size[0] || (view != "" && lipgloss.Height(view) > size[1]) {
					t.Fatalf("view %dx%d exceeds %v", lipgloss.Width(view), lipgloss.Height(view), size)
				}
				d.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			}
		})
	}
}

// longDetailFixture includes unbroken text and multi-codepoint terminal graphemes.
func longDetailFixture() protocol.Connection {
	c := detailFixture()
	c.Metadata.Host = strings.Repeat("long-domain", 15) + ".test"
	c.Metadata.ProcessPath = strings.Repeat("路径/", 30) + "chrome.exe"
	c.Chains = []string{strings.Repeat("日本🇯🇵👩‍💻", 25), "final-node"}
	return c
}

// TestDetail_WrappingPreservesCharacters detects content loss independently of bounds.
func TestDetail_WrappingPreservesCharacters(t *testing.T) {
	c := longDetailFixture()
	view := stripConnANSI(NewDetail(c, false).View(36, 600))
	flatten := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) || strings.ContainsRune("│╭╮╰╯─", r) {
				return -1
			}
			return r
		}, s)
	}
	for _, tc := range []struct{ name, want string }{
		{"host", c.Metadata.Host},
		{"process path", c.Metadata.ProcessPath},
		{"Unicode proxy name", strings.Repeat("日本[JP]👩‍💻", 25)},
		{"connection ID", c.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(flatten(view), flatten(tc.want)) {
				t.Errorf("wrapped field lost characters: %q", tc.want)
			}
		})
	}
}

// TestDetail_ScrollAndResize guards tail access and removal of excess scroll offset.
func TestDetail_ScrollAndResize(t *testing.T) {
	d := NewDetail(detailFixture(), false)
	d.View(60, 16)
	for range 200 {
		d.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	bottom := d.View(60, 16)
	if !strings.Contains(stripConnANSI(bottom), "6e84b12fa205") {
		t.Fatal("last field is unreachable")
	}
	d.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if d.View(60, 16) == bottom {
		t.Fatal("one Up must move immediately after repeated Down at bottom")
	}
	d.View(100, 100)
	if d.scroll != 0 {
		t.Fatal("resize did not clamp stored scroll")
	}
	d.View(36, 12)
	for range 200 {
		d.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	d.Refresh(protocol.Connection{ID: "short"}, false)
	bottom = d.View(36, 12)
	d.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if d.View(36, 12) == bottom {
		t.Fatal("refresh retained excessive scroll offset")
	}
}

// TestModel_DetailPaused verifies frozen observations and their application on resume.
func TestModel_DetailPaused(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(100, 40)
	m.Observe(protocol.ConnectionList{Connections: []protocol.Connection{detailFixture()}}, time.Unix(1, 0))
	m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	m.focus = pageFocus{kind: focusRow, rowID: detailFixture().ID}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Observe(protocol.ConnectionList{}, time.Unix(2, 0))
	if !strings.Contains(stripConnANSI(m.View()), "Paused") || m.detail.closed {
		t.Fatal("paused details must identify frozen observations")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !m.paused {
		t.Fatal("return must preserve Pause")
	}
	m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if m.paused || len(m.history.Active()) != 0 || len(m.history.Closed()) != 1 {
		t.Fatal("resume must apply pending observation")
	}
}

// TestModel_DetailLifecycleAndReturn exercises observation updates without mutations.
func TestModel_DetailLifecycleAndReturn(t *testing.T) {
	for _, closeKey := range []rune{tea.KeyEnter, tea.KeyEsc} {
		t.Run(fmt.Sprint(closeKey), func(t *testing.T) {
			client := &fakeConnectionsClient{}
			m := New(client, nil)
			m.SetSize(80, 24)
			c := detailFixture()
			m.Observe(protocol.ConnectionList{Connections: []protocol.Connection{c}}, time.Unix(1, 0))
			m.focus = pageFocus{kind: focusRow, rowID: c.ID}
			m.query, m.sortColumn, m.sortDirection = "api", "traffic", sortDescending
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			offset := m.detail.scroll
			c.Download += 2048
			m.Observe(protocol.ConnectionList{Connections: []protocol.Connection{c}}, time.Unix(2, 0))
			if m.detail.scroll != offset || m.detail.connection.Download != c.Download {
				t.Fatal("refresh lost position or observation")
			}
			m.Update(tea.KeyPressMsg{Code: closeKey})
			if m.detail != nil || m.focus.rowID != c.ID || m.query != "api" || m.sortColumn != "traffic" || m.sortDirection != sortDescending || client.closedID != "" {
				t.Fatal("return changed list state or closed a connection")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m.Observe(protocol.ConnectionList{}, time.Unix(3, 0))
			if !m.detail.closed || m.detail.connection.Download != c.Download || !m.detail.connection.ClosedAt.Equal(time.Unix(3, 0)) {
				t.Fatal("detail did not retain final observation on close")
			}
		})
	}
}

// TestModel_DetailIgnoresOtherConnectionGeoIP rejects a previous selection's late result.
func TestModel_DetailIgnoresOtherConnectionGeoIP(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(100, 40)
	m.openDetail(protocol.Connection{ID: "A"})
	m.openDetail(protocol.Connection{ID: "B", Metadata: protocol.ConnectionMetadata{DestinationIP: "203.0.113.1"}})
	m.Update(geoIPResultMsg{connectionID: "A", records: []protocol.GeoIPRecord{{Organization: "old-address"}}})
	if strings.Contains(stripConnANSI(m.View()), "old-address") {
		t.Fatal("old connection lookup leaked into new detail")
	}
	m.Update(geoIPResultMsg{connectionID: "B", records: []protocol.GeoIPRecord{{Address: "203.0.113.1", Organization: "current-address"}}})
	if !strings.Contains(stripConnANSI(m.View()), "current-address") {
		t.Fatal("current connection lookup was not applied")
	}
}
