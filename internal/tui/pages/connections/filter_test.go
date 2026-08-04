package connections

import (
	"testing"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

func TestFilter_SearchesVisibleColumnValues(t *testing.T) {
	connection := protocol.Connection{
		ID: "one", Rule: "RuleSet", RulePay: "OpenAI",
		Chains: []string{"GLOBAL", "Streaming", "Japan 01"},
		Metadata: protocol.ConnectionMetadata{
			Host: "chatgpt.com", SourceIP: "127.0.0.1", DestinationIP: "172.64.1.1",
			Process: "codex.exe", InboundName: "DEFAULT-MIXED", SniffHost: "api.openai.com",
		},
	}
	visible := []string{"host", "network", "source", "destination", "chain", "rule", "process", "traffic"}
	for _, query := range []string{"chatgpt", "127.0.0.1", "RuleSet", "OpenAI", "Streaming", "Japan 01", "codex"} {
		if !matchesConnection(connection, query, allSources, visible) {
			t.Fatalf("query=%q did not match visible columns", query)
		}
	}
	// Hidden / non-column fields must not match when not in visible set.
	hiddenOnly := []string{"host"}
	if matchesConnection(connection, "codex", allSources, hiddenOnly) {
		t.Fatal("process match should not apply when process column is hidden")
	}
	if matchesConnection(connection, "DEFAULT-MIXED", allSources, visible) {
		t.Fatal("inbound name is not a table column and must not match")
	}
	if matchesConnection(connection, "missing", allSources, visible) {
		t.Fatal("unexpected match")
	}
}

func TestConnections_SearchUsesVisibleColumnsOnly(t *testing.T) {
	model := New(nil, nil)
	model.SetPreferences(protocol.TUIPreferences{ConnectionsColumns: []string{"host"}})
	model.Observe(protocol.ConnectionList{Connections: []protocol.Connection{{
		ID: "one", Metadata: protocol.ConnectionMetadata{Host: "visible.host", Process: "secret-process.exe"},
	}}}, time.Unix(1, 0))
	model.query = "secret-process"
	if rows := model.visibleRows(); len(rows) != 0 {
		t.Fatalf("hidden process column should not match search: rows=%v", rows)
	}
	model.query = "visible.host"
	if rows := model.visibleRows(); len(rows) != 1 || rows[0].ID != "one" {
		t.Fatalf("visible host should match: rows=%v", rows)
	}
}

func TestSourceOptionsAreSortedAndFallbackToAll(t *testing.T) {
	connections := []protocol.Connection{
		{Metadata: protocol.ConnectionMetadata{SourceIP: "10.0.0.2"}},
		{Metadata: protocol.ConnectionMetadata{SourceIP: "10.0.0.1"}},
		{Metadata: protocol.ConnectionMetadata{SourceIP: "10.0.0.2"}},
	}
	options := sourceOptions(connections)
	if len(options) != 3 || options[0] != allSources || options[1] != "10.0.0.1" || options[2] != "10.0.0.2" {
		t.Fatalf("options=%v", options)
	}
	if got := validSource("disappeared", options); got != allSources {
		t.Fatalf("source=%q", got)
	}
}
