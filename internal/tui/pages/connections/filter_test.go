package connections

import (
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

func TestFilter_SearchesEveryAcceptedSafeFieldAndFullChain(t *testing.T) {
	connection := protocol.Connection{
		ID: "one", Rule: "RuleSet", RulePay: "OpenAI",
		Chains: []string{"GLOBAL", "Streaming", "Japan 01"},
		Metadata: protocol.ConnectionMetadata{
			Host: "chatgpt.com", SourceIP: "127.0.0.1", DestinationIP: "172.64.1.1",
			Process: "codex.exe", InboundName: "DEFAULT-MIXED", SniffHost: "api.openai.com",
		},
	}
	for _, query := range []string{"chatgpt", "127.0.0.1", "172.64", "RuleSet", "OpenAI", "Streaming", "Japan 01", "codex", "DEFAULT-MIXED", "api.openai"} {
		if !matchesConnection(connection, query, allSources) {
			t.Fatalf("query=%q did not match", query)
		}
	}
	if matchesConnection(connection, "missing", allSources) {
		t.Fatal("unexpected match")
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
