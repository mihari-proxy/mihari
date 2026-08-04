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
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view := model.View()
	for _, want := range []string{"Active 1", "Source IP: All", "Columns", "Pause", "GLOBAL \u2192 Streaming \u2192 Auto Select \u2192 Japan 01"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view does not contain %q: %s", want, view)
		}
	}
}

func TestModel_DualLineKeepsFullChainInModelAndShowsHost(t *testing.T) {
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
		t.Fatalf("primary host missing: %s", view)
	}
	if !strings.Contains(view, "GLOBAL") || !strings.Contains(view, "Japan 01") {
		t.Fatalf("secondary chain missing: %s", view)
	}
	if got := strings.Join(model.visibleRows()[0].Chains, " / "); got != "GLOBAL / Streaming / Auto Select / Japan 01" {
		t.Fatalf("chain=%q", got)
	}
}

func TestModel_CompactHidesRuleAndProcessOnSecondary(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(70, 16) // Compact < 90
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{
		ID: "one", Chains: []string{"DIRECT"}, Rule: "MATCH", RulePay: "final",
		Metadata: protocol.ConnectionMetadata{Host: "a.test", Process: "chrome.exe", Type: "HTTPS", Network: "tcp"},
	}}}, time.Unix(1, 0))
	view := model.View()
	if strings.Contains(view, "chrome.exe") || strings.Contains(view, "MATCH") {
		t.Fatalf("compact should hide rule/process: %s", view)
	}
	model.SetSize(100, 16)
	view = model.View()
	if !strings.Contains(view, "chrome.exe") || !strings.Contains(view, "MATCH") {
		t.Fatalf("full should show rule/process: %s", view)
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

func TestView_FocusedRowHighlightOnlyWhenContentFocused(t *testing.T) {
	model := New(nil, nil)
	model.SetPreferences(protocol.TUIPreferences{ConnectionsColumns: []string{"host"}})
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{
		{ID: "one", Metadata: protocol.ConnectionMetadata{Host: "one.test"}},
		{ID: "two", Metadata: protocol.ConnectionMetadata{Host: "two.test"}},
	}}, time.Unix(1, 0))
	model.focus = pageFocus{kind: focusRow, rowID: "two"}

	model.SetContentFocused(false)
	for _, line := range strings.Split(model.View(), "\n") {
		if strings.Contains(line, "two.test") {
			if !strings.Contains(line, ui.FocusMarker) {
				t.Fatalf("row marker missing while rail-focused: %q", line)
			}
			if strings.Contains(line, "\x1b[") {
				t.Fatalf("row should not use accent while rail owns focus: %q", line)
			}
		}
	}

	model.SetContentFocused(true)
	var focused string
	for _, line := range strings.Split(model.View(), "\n") {
		if strings.Contains(line, "two.test") {
			focused = line
		}
	}
	if focused == "" || !strings.Contains(focused, ui.FocusMarker) || !strings.Contains(focused, "\x1b[") {
		t.Fatalf("focused content row missing highlight: %q", focused)
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

	model.SetContentFocused(false)
	railControl := strings.Split(model.View(), "\n")[0]
	if strings.Contains(railControl, "\x1b[") {
		t.Fatalf("control strip should stay plain while rail owns focus: %q", railControl)
	}

	model.SetContentFocused(true)
	focusedControl := strings.Split(model.View(), "\n")[0]
	if !strings.Contains(focusedControl, "\x1b[") {
		t.Fatalf("active control chip should highlight when content focused: %q", focusedControl)
	}
	if focusedControl == railControl {
		t.Fatal("content-focused control strip should differ from rail-focused")
	}

	// Header focus also gets a visible accent.
	model.focus = pageFocus{kind: focusHeader}
	model.headerIndex = 0
	headerLine := strings.Split(model.View(), "\n")[2]
	if !strings.Contains(headerLine, "\x1b[") {
		t.Fatalf("focused header should highlight: %q", headerLine)
	}
}

func TestConnections_SearchNotInControlStrip(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(100, 24)
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{
		ID: "one", Metadata: protocol.ConnectionMetadata{Host: "example.com"},
	}}}, time.Unix(1, 0))
	view := model.View()
	lines := strings.Split(view, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected control strip + search bar, got %d lines: %s", len(lines), view)
	}
	control := lines[0]
	if strings.Contains(control, "Search") || strings.Contains(control, "/ ") {
		t.Fatalf("control strip must not embed search: %q", control)
	}
	for _, want := range []string{"Active", "Source IP", "Columns", "Pause"} {
		if !strings.Contains(control, want) {
			t.Fatalf("control missing %q: %q", want, control)
		}
	}
	search := lines[1]
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
	_, leave := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.searching {
		t.Fatal("enter should leave search")
	}
	if leave == nil {
		t.Fatal("expected input mode restore command")
	}
	mode, ok := leave().(ui.InputModeMsg)
	if !ok || mode.Mode != ui.InputNavigation {
		t.Fatalf("mode=%#v", mode)
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
