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

func TestModel_RowLeftRightScrollsWithoutTruncatingChainState(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(32, 16)
	model.SetPreferences(protocol.TUIPreferences{ConnectionsColumns: []string{"host", "chain"}})
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{
		ID: "one", Chains: []string{"GLOBAL", "Streaming", "Auto Select", "Japan 01"},
		Metadata: protocol.ConnectionMetadata{Host: "example.com"},
	}}}, time.Unix(1, 0))
	model.focus = pageFocus{kind: focusRow, rowID: "one"}
	for range 8 {
		model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	}
	if !strings.Contains(model.View(), "Japan 01") {
		t.Fatalf("scrolled view lost chain tail: %s", model.View())
	}
	if got := strings.Join(model.visibleRows()[0].Chains, " / "); got != "GLOBAL / Streaming / Auto Select / Japan 01" {
		t.Fatalf("chain=%q", got)
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
