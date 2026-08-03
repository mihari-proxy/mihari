package connections

import (
	"context"
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
	request, ok := command().(ui.ConfirmationRequestMsg)
	if !ok || request.OnConfirm == nil {
		t.Fatalf("message=%T request=%#v", command(), request)
	}
}

type fakeConnectionsClient struct{ closedID string }

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
