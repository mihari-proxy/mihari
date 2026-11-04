package proxies

import (
	"fmt"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestSetGroups_UnchangedSnapshotSkipsRebuild(t *testing.T) {
	m := New(nil, nil)
	revision := uint64(1)
	snapshot := protocol.ProxyGroups{Revision: &revision, SubscriptionID: "sub", Groups: []protocol.ProxyGroup{{Name: "group", Now: "a", All: []string{"a"}, Nodes: []protocol.ProxyNode{{Name: "a"}}}}}
	m.SetGroups(snapshot)
	before := &m.groups[0]
	m.autoDirty = false
	m.SetGroups(snapshot)
	if &m.groups[0] != before || m.autoDirty {
		t.Fatal("identical snapshot rebuilt groups or dirtied auto latency")
	}
	// Local selection is optimistic; a subsequent authoritative snapshot must restore it.
	m.groups[0].Now = "optimistic"
	m.SetGroups(snapshot)
	if m.groups[0].Now != "a" || !m.autoDirty {
		t.Fatal("authoritative selection not applied")
	}
	m.autoDirty = false
	revision++
	m.SetGroups(snapshot)
	if *m.groupsRevision != revision || !m.autoDirty {
		t.Fatal("revision-only change lost")
	}
	m.autoDirty = false
	m.InvalidateGroups()
	m.loadError = "offline"
	m.SetGroups(snapshot)
	if !m.groupsFresh || m.loadError != "" || !m.autoDirty {
		t.Fatal("freshness/error recovery lost")
	}
}

func TestSetGroups_EquivalentDuplicateNodesSkipRebuild(t *testing.T) {
	m := New(nil, nil)
	snapshot := protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "group", Nodes: []protocol.ProxyNode{{Name: "a"}, {Name: "a", Type: "ignored"}}}}}
	m.SetGroups(snapshot)
	before := &m.groups[0]
	m.autoDirty = false
	m.SetGroups(snapshot)
	if before != &m.groups[0] || m.autoDirty {
		t.Fatal("equivalent duplicate nodes rebuilt groups")
	}
	snapshot.Groups[0].Nodes[0].Type = "changed"
	m.SetGroups(snapshot)
	if m.groups[0].Nodes[0].Type != "changed" {
		t.Fatal("first node change lost")
	}
}

func BenchmarkSetGroups_Unchanged(b *testing.B) {
	m := New(nil, nil)
	nodes := make([]protocol.ProxyNode, 1000)
	for i := range nodes {
		nodes[i].Name = fmt.Sprintf("node-%d", i)
	}
	snapshot := protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "group", Nodes: nodes}}}
	m.SetGroups(snapshot)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		m.SetGroups(snapshot)
	}
}
