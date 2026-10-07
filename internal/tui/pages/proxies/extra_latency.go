package proxies

import "github.com/mihari-proxy/mihari/internal/control/protocol"

// SetPreferences applies committed page preferences without changing selections.
func (m *Model) SetPreferences(prefs protocol.TUIPreferences) {
	m.autoDirty = true
	next := prefs.EffectiveProxies()
	m.concurrencyChanged = m.concurrencyChanged || next.LatencyTestConcurrency != m.preferences.LatencyTestConcurrency
	m.preferences = next
}

// selectedLeaf follows current selections, rejecting missing members and cycles.
func (m *Model) selectedLeaf(name string) string {
	seen := make(map[string]bool)
	for name != "" && !seen[name] {
		seen[name] = true
		if i := m.groupIndex(name); i >= 0 {
			group := m.groups[i]
			if nodeIndex(group.Nodes, group.Now) < 0 {
				return ""
			}
			name = group.Now
			continue
		}
		for _, group := range m.groups {
			if nodeIndex(group.Nodes, name) >= 0 {
				return name
			}
		}
		return ""
	}
	return ""
}

func (m *Model) extraLatency(name string) string {
	if !m.preferences.ExtraLatency {
		return ""
	}
	leaf := m.selectedLeaf(name)
	if leaf == "" {
		return ""
	}
	return " " + renderDelay(m.theme, m.delays[leaf], m.now)
}
