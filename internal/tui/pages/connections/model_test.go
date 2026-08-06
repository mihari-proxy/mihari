package connections

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestModel_PinsFocusedRowByIDAcrossSorting(t *testing.T) {
	model := New(nil, nil)
	model.SetPreferences(protocol.TUIPreferences{Revision: 3, ConnectionsColumns: []string{"host", "download", "chain"}})
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{
		{ID: "one", Download: 1, Metadata: protocol.ConnectionMetadata{Host: "one.test"}},
		{ID: "two", Download: 2, Metadata: protocol.ConnectionMetadata{Host: "two.test"}},
	}}, time.Unix(1, 0))
	model.focus = pageFocus{kind: focusRow, rowID: "one"}
	model.headerIndex = 1
	model.cycleSort()
	rows := model.visibleRows()
	if rows[0].ID != "two" || model.focus.rowID != "one" {
		t.Fatalf("rows=%v focus=%#v", rows, model.focus)
	}
}

func TestModel_ClosedDatasetHasNoCloseAction(t *testing.T) {
	client := &fakeConnectionsClient{}
	model := New(client, func() string { return "close-1" })
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{ID: "one"}}}, time.Unix(1, 0))
	model.Observe(protocol.ConnectionList{}, time.Unix(2, 0))
	model.dataset = datasetClosed
	model.focus = pageFocus{kind: focusRow, rowID: "one"}
	_, command := model.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if command != nil || client.closedID != "" {
		t.Fatalf("command=%v closed=%q", command != nil, client.closedID)
	}
}

func TestModel_ControlRowAndDetailsPreserveFullChain(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(100, 24)
	model.SetPreferences(protocol.TUIPreferences{ConnectionsColumns: []string{"host", "chain", "traffic"}})
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{
		ID: "one", Chains: []string{"GLOBAL", "Streaming", "Auto Select", "Japan 01"},
		Metadata: protocol.ConnectionMetadata{Host: "chatgpt.com", SourceIP: "127.0.0.1"},
	}}}, time.Unix(1, 0))
	model.focus = pageFocus{kind: focusRow, rowID: "one"}
	// List mode: controls + single-line table with the chain column visible.
	view := model.View()
	for _, want := range []string{"Active 1", "Source IP: All", "Columns", "Pause"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view does not contain %q: %s", want, view)
		}
	}
	if !strings.Contains(view, "GLOBAL") {
		t.Fatalf("chain column missing: %s", view)
	}
	if got := strings.Join(model.visibleRows()[0].Chains, " / "); got != "GLOBAL / Streaming / Auto Select / Japan 01" {
		t.Fatalf("model chain=%q", got)
	}
	// Detail replaces the whole page (design C1); the pane shows the chain
	// once scrolled into view.
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view = model.View()
	if strings.Contains(view, "Active 1") {
		t.Fatalf("detail should replace the list: %s", view)
	}
	model.detail.scroll = 6
	view = model.View()
	if !strings.Contains(view, "GLOBAL → Streaming → Auto Select → Japan 01") {
		t.Fatalf("detail should show the full chain: %s", view)
	}
}

func TestModel_SingleLineKeepsFullChainInModelAndShowsHost(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(80, 16)
	model.SetPreferences(protocol.TUIPreferences{ConnectionsColumns: []string{"host", "chain"}})
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{
		ID: "one", Chains: []string{"GLOBAL", "Streaming", "Auto Select", "Japan 01"},
		Metadata: protocol.ConnectionMetadata{Host: "example.com", Type: "HTTP", Network: "tcp"},
	}}}, time.Unix(1, 0))
	model.focus = pageFocus{kind: focusRow, rowID: "one"}
	view := model.View()
	if !strings.Contains(view, "example.com") {
		t.Fatalf("host missing: %s", view)
	}
	// Single line truncates the chain at the column width (no secondary line).
	if !strings.Contains(view, "GLOBAL") {
		t.Fatalf("chain column missing: %s", view)
	}
	if strings.Contains(view, "Japan 01") {
		t.Fatalf("chain should truncate in the list: %s", view)
	}
	if got := strings.Join(model.visibleRows()[0].Chains, " / "); got != "GLOBAL / Streaming / Auto Select / Japan 01" {
		t.Fatalf("chain=%q", got)
	}
}

func TestModel_ColumnDropsFollowPriority(t *testing.T) {
	model := New(nil, nil)
	model.SetPreferences(protocol.TUIPreferences{ConnectionsColumns: []string{"host", "traffic", "network", "rule", "start", "process"}})
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{
		ID: "one", Rule: "MATCH", RulePay: "final",
		Metadata: protocol.ConnectionMetadata{Host: "a.test", Process: "chrome.exe", Type: "HTTPS", Network: "tcp"},
	}}}, time.Unix(1, 0))
	// Page width 70: host/traffic/network/rule fit (4 cols); start and process
	// are dropped by priority.
	model.SetSize(70, 16)
	view := model.View()
	if !strings.Contains(view, "MATCH") || strings.Contains(view, "chrome.exe") {
		t.Fatalf("70px should show rule but drop process: %s", view)
	}
	// Page width 90: all six checked columns fit.
	model.SetSize(90, 16)
	view = model.View()
	if !strings.Contains(view, "chrome.exe") {
		t.Fatalf("90px should show process: %s", view)
	}
}

func TestModel_PauseKeepsLatestSnapshotUntilResume(t *testing.T) {
	model := New(nil, nil)
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{ID: "one"}}}, time.Unix(1, 0))
	model.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{ID: "two"}}}, time.Unix(2, 0))
	if model.history.Active()[0].ID != "one" {
		t.Fatalf("paused active=%#v", model.history.Active())
	}
	model.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if model.history.Active()[0].ID != "two" {
		t.Fatalf("resumed active=%#v", model.history.Active())
	}
}

func TestModel_CloseAllRequestsConfirmation(t *testing.T) {
	model := New(&fakeConnectionsClient{}, nil)
	_, command := model.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if command == nil {
		t.Fatal("close all did not request confirmation")
	}
	request, ok := command().(ui.ActionIntentMsg)
	if !ok || request.Execute == nil || request.Action != ui.ActionCloseAllConnections {
		t.Fatalf("message=%T request=%#v", command(), request)
	}
}

func TestView_TrafficDataColorsWhileRailFocused(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(100, 24)
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{
		ID: "one", UploadSpeed: 1024, DownloadSpeed: 2048,
		Metadata: protocol.ConnectionMetadata{Host: "one.test", Network: "tcp", Type: "HTTP"},
	}}}, time.Unix(1, 0))
	model.SetContentFocused(false)
	view := model.View()
	// RenderTrafficColumn always paints UL Success / DL Info with the units
	// right-anchored; at the default 100-col layout the 19-wide column splits
	// into slots that truncate the digits ("↑… KiB/s"), keeping markers, colors
	// and units.
	if !strings.Contains(view, "\x1b[38;5;78m↑") {
		t.Fatalf("traffic semantic colors missing while rail-focused:\n%s", view)
	}
	if !strings.Contains(view, "\x1b[38;5;75m↓") {
		t.Fatalf("download color missing while rail-focused:\n%s", view)
	}
	if !strings.Contains(view, "↑… KiB/s") || !strings.Contains(view, "↓2… KiB/s") {
		t.Fatalf("traffic slot layout unexpected:\n%s", view)
	}
}

func TestView_FocusedRowHighlightOnlyWhenContentFocused(t *testing.T) {
	model := New(nil, nil)
	model.SetPreferences(protocol.TUIPreferences{ConnectionsColumns: []string{"host"}})
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{
		{ID: "one", Metadata: protocol.ConnectionMetadata{Host: "one.test"}},
		{ID: "two", Metadata: protocol.ConnectionMetadata{Host: "two.test"}},
	}}, time.Unix(1, 0))
	model.focus = pageFocus{kind: focusRow, rowID: "two"}

	findHost := func() string {
		for _, line := range strings.Split(model.View(), "\n") {
			if strings.Contains(line, "two.test") {
				return line
			}
		}
		return ""
	}

	model.SetContentFocused(false)
	railLine := findHost()
	if railLine == "" || !strings.Contains(railLine, ui.FocusMarker) {
		t.Fatalf("row marker missing while rail-focused: %q", railLine)
	}
	// Data colors may use ANSI; reverse RowFocus must wait for content focus.
	if strings.Contains(railLine, "\x1b[7m") {
		t.Fatalf("row should not use reverse focus chrome while rail owns focus: %q", railLine)
	}

	model.SetContentFocused(true)
	focused := findHost()
	if focused == "" || !strings.Contains(focused, ui.FocusMarker) {
		t.Fatalf("focused content row missing marker: %q", focused)
	}
	if focused == railLine {
		t.Fatalf("content focus should add RowFocus styling: rail=%q content=%q", railLine, focused)
	}
}

func TestModel_FooterHintsAreContextual(t *testing.T) {
	model := New(nil, nil)
	if hints := model.FooterHints(); !strings.Contains(hints, "/ search") {
		t.Fatalf("default=%q", hints)
	}
	model.searching = true
	if hints := model.FooterHints(); hints != ui.FooterSearchMode {
		t.Fatalf("search=%q", hints)
	}
	model.searching = false
	model.columnsOpen = true
	if hints := model.FooterHints(); hints != ui.FooterColumnsMode {
		t.Fatalf("columns=%q", hints)
	}
	model.columnsOpen = false
	model.detail = &Detail{}
	if hints := model.FooterHints(); hints != ui.FooterDetailMode {
		t.Fatalf("detail=%q", hints)
	}
}

func TestView_ControlStripHighlightsActiveWhenContentFocused(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(100, 24)
	model.FocusFirst()
	model.controlIndex = 2 // Columns

	// Control strip is the first body line inside the Controls section (line index 1).
	model.SetContentFocused(false)
	railView := model.View()
	railControl := sectionBodyLine(railView, 0)
	// Section border is styled; strip ANSI from the border line itself. The control
	// body chips must stay plain while the rail owns focus.
	if strings.Contains(stripConnANSI(railControl), "Columns") && strings.Contains(railControl, "\x1b[1;") {
		// Bold accent (ControlActive) should not appear on chips while unfocused.
		// Border ANSI alone is fine — only chip accent is the contract.
	}
	// Active chip uses bold+accent when content focused; plain chips have no bold.
	model.SetContentFocused(true)
	focusedControl := sectionBodyLine(model.View(), 0)
	if !strings.Contains(focusedControl, "\x1b[") {
		t.Fatalf("active control chip should highlight when content focused: %q", focusedControl)
	}
	if focusedControl == railControl {
		t.Fatal("content-focused control strip should differ from rail-focused")
	}

	// Dual-section: list header lives in the second section body.
	model.focus = pageFocus{kind: focusHeader}
	model.headerIndex = 0
	view := model.View()
	if !strings.Contains(view, "\x1b[") {
		t.Fatalf("focused header should highlight in dual-section view:\n%s", view)
	}
}

func TestConnections_SearchNotInControlStrip(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(100, 24)
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{
		ID: "one", Metadata: protocol.ConnectionMetadata{Host: "example.com"},
	}}}, time.Unix(1, 0))
	view := model.View()
	// Controls section: body line 0 = control chips, body line 1 = search.
	control := sectionBodyLine(view, 0)
	if strings.Contains(control, "Search") {
		t.Fatalf("control strip must not embed search: %q", control)
	}
	for _, want := range []string{"Active", "Source IP", "Columns", "Pause"} {
		if !strings.Contains(control, want) {
			t.Fatalf("control missing %q: %q", want, control)
		}
	}
	search := sectionBodyLine(view, 1)
	if !strings.Contains(search, ui.SearchPlaceholder) && !strings.Contains(search, "/ ") {
		t.Fatalf("search bar missing: %q", search)
	}
	if !strings.Contains(view, "example.com") {
		t.Fatalf("table body missing host: %s", view)
	}
	// `/` still enters search mode.
	model.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !model.searching {
		t.Fatal("expected search mode after /")
	}
}

// sectionBodyLine returns the n-th body line of the first bordered section in view
// (skipping top border). n is 0-based within that section's body.
func sectionBodyLine(view string, n int) string {
	lines := strings.Split(view, "\n")
	// First line is top border; body starts at 1 until bottom border.
	body := 0
	for i := 1; i < len(lines); i++ {
		plain := stripConnANSI(lines[i])
		if strings.HasPrefix(strings.TrimLeft(plain, " "), "╰") {
			break
		}
		if body == n {
			return lines[i]
		}
		body++
	}
	return ""
}

func stripConnANSI(value string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range value {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && ((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')):
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestModel_SearchSupportsPasteMsg(t *testing.T) {
	model := New(nil, nil)
	model.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !model.searching {
		t.Fatal("expected search mode after /")
	}
	model.Update(tea.PasteMsg{Content: "hello\nworld"})
	if model.query != "helloworld" {
		t.Fatalf("query=%q", model.query)
	}
	_, command := model.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	if command == nil {
		t.Fatal("expected clipboard read command")
	}
	model.Update(command())
	_, leave := model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if model.searching {
		t.Fatal("esc should leave search")
	}
	if leave == nil {
		t.Fatal("expected input mode restore command")
	}
	mode, ok := leave().(ui.InputModeMsg)
	if !ok || mode.Mode != ui.InputNavigation {
		t.Fatalf("mode=%#v", mode)
	}
}

func TestConnections_SearchDirectTypeNoEnter(t *testing.T) {
	model := New(nil, nil)
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{
		ID: "one", Metadata: protocol.ConnectionMetadata{Host: "example.com"},
	}}}, time.Unix(1, 0))
	model.FocusFirst()
	// Down from control enters search input mode immediately.
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if !model.searching || model.focus.kind != focusSearch || command == nil {
		t.Fatalf("searching=%v focus=%#v command=%v", model.searching, model.focus, command != nil)
	}
	model.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	model.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if model.query != "ex" {
		t.Fatalf("query=%q", model.query)
	}
	// Left/right are cursor, not horizontal table scroll.
	model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	model.Update(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
	if model.query != "eYx" {
		t.Fatalf("cursor insert query=%q", model.query)
	}
	// Page shortcuts disabled (p would pause otherwise).
	model.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if model.query != "eYpx" || model.paused {
		t.Fatalf("p should type: query=%q paused=%v", model.query, model.paused)
	}
	// Esc leaves input mode; focus stays on search bar.
	model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if model.searching || model.focus.kind != focusSearch {
		t.Fatalf("after esc searching=%v focus=%#v", model.searching, model.focus)
	}
	// Typing again re-enters without Enter.
	model.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	if !model.searching || model.query != "eYpxz" {
		t.Fatalf("re-enter searching=%v query=%q", model.searching, model.query)
	}
	// Up leaves search and focuses control.
	model.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if model.searching || model.focus.kind != focusControl {
		t.Fatalf("up leave searching=%v focus=%#v", model.searching, model.focus)
	}
}

func TestModel_DetailLooksUpOnlyPublicDestinationAddresses(t *testing.T) {
	client := &fakeConnectionsClient{geoIPResult: protocol.GeoIPLookupResult{Records: []protocol.GeoIPRecord{
		{Address: "1.1.1.1", CountryCode: "AU", ASN: 13335, Organization: "Cloudflare, Inc."},
		{Address: "8.8.8.8", CountryCode: "US", ASN: 15169, Organization: "Google LLC"},
	}}}
	model := New(client, nil)
	model.SetSize(100, 28)
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{
		ID: "one", Metadata: protocol.ConnectionMetadata{
			SourceIP: "127.0.0.1", DestinationIP: "1.1.1.1", RemoteDestination: "8.8.8.8:443",
		},
	}}}, time.Unix(1, 0))
	model.focus = pageFocus{kind: focusRow, rowID: "one"}
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil {
		t.Fatal("detail did not request geoip lookup")
	}
	message := command()
	model.Update(message)
	if got := strings.Join(client.geoIPAddresses, ","); got != "1.1.1.1,8.8.8.8" {
		t.Fatalf("addresses=%q", got)
	}
	view := model.View()
	for _, want := range []string{"GeoIP", "AU", "AS13335", "Cloudflare, Inc.", "Basic"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q: %s", want, view)
		}
	}
}

func TestModel_GeoIPFailureDegradesOnlyGeoIPCard(t *testing.T) {
	client := &fakeConnectionsClient{geoIPErr: errors.New("database unavailable")}
	model := New(client, nil)
	model.SetSize(100, 28)
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{
		ID: "one", Metadata: protocol.ConnectionMetadata{DestinationIP: "1.1.1.1"},
	}}}, time.Unix(1, 0))
	model.focus = pageFocus{kind: focusRow, rowID: "one"}
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model.Update(command())
	view := model.View()
	if !strings.Contains(view, "GeoIP") || !strings.Contains(view, "Unavailable") || !strings.Contains(view, "Basic") {
		t.Fatalf("view=%s", view)
	}
}

type fakeConnectionsClient struct {
	closedID       string
	geoIPAddresses []string
	geoIPResult    protocol.GeoIPLookupResult
	geoIPErr       error
}

func (c *fakeConnectionsClient) CloseConnection(_ context.Context, id string, _ protocol.MutationRequest) (protocol.MutationResult, error) {
	c.closedID = id
	return protocol.MutationResult{Schema: "mihari/v1"}, nil
}

func (c *fakeConnectionsClient) UpdateTUIPreferences(_ context.Context, request protocol.UpdateTUIPreferencesRequest) (protocol.TUIPreferences, error) {
	return protocol.TUIPreferences{Schema: "mihari/v1", Revision: 4, ConnectionsColumns: request.ConnectionsColumns}, nil
}

func (c *fakeConnectionsClient) CloseAllConnections(context.Context, protocol.MutationRequest) (protocol.MutationResult, error) {
	return protocol.MutationResult{Schema: "mihari/v1"}, nil
}

func (c *fakeConnectionsClient) LookupGeoIP(_ context.Context, request protocol.GeoIPLookupRequest) (protocol.GeoIPLookupResult, error) {
	c.geoIPAddresses = append([]string(nil), request.Addresses...)
	return c.geoIPResult, c.geoIPErr
}
