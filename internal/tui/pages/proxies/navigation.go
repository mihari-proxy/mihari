package proxies

import "github.com/LeeShunEE/mihari/internal/control/protocol"

type FocusID struct {
	Group string
	Node  string
}

func (m *Model) move(key string) {
	if m.focus.Group == "" || len(m.groups) == 0 {
		return
	}
	if m.focus.Node == "" {
		m.moveGroup(key)
	} else {
		m.moveNode(key)
	}
	m.ensureFocusVisible()
}

func (m *Model) moveGroup(key string) {
	items := m.visibleItems()
	index := indexOfFocus(items, m.focus)
	switch key {
	case "up":
		if index > 0 {
			m.focus = items[index-1]
		}
	case "down":
		if index >= 0 && index+1 < len(items) {
			m.focus = items[index+1]
		}
	}
}

func (m *Model) moveNode(key string) {
	groupIndex := m.groupIndex(m.focus.Group)
	if groupIndex < 0 {
		return
	}
	nodes := m.groups[groupIndex].Nodes
	nodeIndex := nodeIndex(nodes, m.focus.Node)
	if nodeIndex < 0 {
		m.focus = FocusID{Group: m.focus.Group}
		return
	}
	columns := m.columns()
	switch key {
	case "left":
		if nodeIndex%columns == 0 {
			m.focus = FocusID{Group: m.focus.Group}
		} else {
			m.focus.Node = nodes[nodeIndex-1].Name
		}
	case "right":
		if nodeIndex%columns < columns-1 && nodeIndex+1 < len(nodes) {
			m.focus.Node = nodes[nodeIndex+1].Name
		}
	case "up":
		if nodeIndex-columns >= 0 {
			m.focus.Node = nodes[nodeIndex-columns].Name
		} else {
			m.focus = FocusID{Group: m.focus.Group}
		}
	case "down":
		nextRowStart := (nodeIndex/columns + 1) * columns
		if nextRowStart < len(nodes) {
			target := min(nodeIndex+columns, len(nodes)-1)
			m.focus.Node = nodes[target].Name
		} else if groupIndex+1 < len(m.groups) {
			m.focus = FocusID{Group: m.groups[groupIndex+1].Name}
		}
	}
}

func (m *Model) visibleItems() []FocusID {
	items := make([]FocusID, 0, len(m.groups))
	for _, group := range m.groups {
		items = append(items, FocusID{Group: group.Name})
		if !m.expanded[group.Name] {
			continue
		}
		for _, node := range group.Nodes {
			items = append(items, FocusID{Group: group.Name, Node: node.Name})
		}
	}
	return items
}

func (m *Model) groupIndex(name string) int {
	for index, group := range m.groups {
		if group.Name == name {
			return index
		}
	}
	return -1
}

func nodeIndex(nodes []protocol.ProxyNode, name string) int {
	for index, node := range nodes {
		if node.Name == name {
			return index
		}
	}
	return -1
}

func indexOfFocus(items []FocusID, focus FocusID) int {
	for index, item := range items {
		if item == focus {
			return index
		}
	}
	return -1
}
