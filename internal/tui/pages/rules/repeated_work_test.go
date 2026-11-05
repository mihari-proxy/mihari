package rules

import (
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestSetSnapshots_UnchangedDataKeepsOwnedSlices(t *testing.T) {
	m := New(nil, nil)
	rules := protocol.RuleList{Rules: []protocol.Rule{{Type: "DOMAIN", Payload: "example.test", Proxy: "DIRECT"}}}
	providers := protocol.RuleProviderList{Revision: 1, Providers: []protocol.RuleProvider{{Name: "provider", RuleCount: 2}}}
	m.SetRules(rules)
	m.SetProviders(providers)
	rule, provider := &m.rules[0], &m.providers[0]
	m.SetRules(rules)
	providers.Revision = 2
	m.SetProviders(providers)
	if rule != &m.rules[0] || provider != &m.providers[0] || m.revision != 2 {
		t.Fatal("unchanged data rebuilt or revision lost")
	}
	rules.Rules[0].Payload = "changed.test"
	providers.Providers[0].RuleCount = 3
	m.SetRules(rules)
	m.SetProviders(providers)
	if m.rules[0].Payload != "changed.test" || m.providers[0].RuleCount != 3 {
		t.Fatal("changed data not applied")
	}
	rules.Rules[0].Payload = "caller mutation"
	if m.rules[0].Payload != "changed.test" {
		t.Fatal("page retained mutable caller slice")
	}
}

func BenchmarkSetRules_Unchanged(b *testing.B) {
	m := New(nil, nil)
	snapshot := protocol.RuleList{Rules: make([]protocol.Rule, 10000)}
	for i := range snapshot.Rules {
		snapshot.Rules[i] = protocol.Rule{Type: "DOMAIN", Payload: "example.test", Proxy: "DIRECT"}
	}
	m.SetRules(snapshot)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		m.SetRules(snapshot)
	}
}
