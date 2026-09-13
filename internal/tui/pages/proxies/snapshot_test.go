package proxies

import (
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestSnapshot_NarrowViewKeepsFailureReasonAndSuccessfulEmptySnapshot(t *testing.T) {
	for _, hasSnapshot := range []bool{false, true} {
		m := New(nil, nil)
		m.SetSize(50, 30)
		if hasSnapshot {
			m.ObserveSnapshot(protocol.ProxyGroups{}, time.Unix(100, 0), nil)
		}
		m.ObserveSnapshot(protocol.ProxyGroups{}, time.Time{}, protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "Failed to load provider nodes: mihomo returned HTTP 503"})
		if !strings.Contains(m.View(), "503") {
			t.Fatal("narrow view clipped the actual HTTP failure")
		}
		if hasSnapshot && !strings.Contains(m.View(), "Stale data") {
			t.Fatal("successful empty snapshot lost its stale state")
		}
	}
}

func TestSnapshot_RetainsMeasuredLatencyAndSeparateSelectionError(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(100, 40)
	good := protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "group", Now: "HK", Nodes: []protocol.ProxyNode{{Name: "HK"}, {Name: "HK"}}}}}
	m.ObserveSnapshot(good, time.Unix(100, 0), nil)
	m.expanded["group"] = true
	m.delays["HK"] = DelayState{Kind: DelayValue, Milliseconds: 42, TestedAt: time.Unix(90, 0)}
	m.lastError = "Selection failed"
	m.ObserveSnapshot(protocol.ProxyGroups{}, time.Time{}, protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "Refresh failed"})
	if len(m.groups[0].Nodes) != 1 || !strings.Contains(m.View(), "42 ms") || !strings.Contains(m.View(), "Last tested") {
		t.Fatal("lost retained measurement or duplicate card")
	}
	m.ObserveSnapshot(good, time.Unix(200, 0), nil)
	if m.delays["HK"].Milliseconds != 42 || !m.delays["HK"].TestedAt.Equal(time.Unix(90, 0)) || m.lastError != "Selection failed" || m.loadError != "" {
		t.Fatal("refresh changed independent operation state")
	}
}
