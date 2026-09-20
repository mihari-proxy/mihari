package proxies

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// pageTarget maps a group header or node card to its rendered vertical span.
type pageTarget struct {
	focus              FocusID
	start, end, column int
}

// movePage moves roughly one viewport through rendered rows, preserving the node column.
func (m *Model) movePage(direction int) {
	if len(m.groups) == 0 || m.height <= 0 {
		return
	}
	var targets []pageTarget
	lines, _, _ := m.buildContentWithTargets(false, &targets)
	focus := m.focus
	focus.Locate = false
	current := -1
	for i, target := range targets {
		if target.focus == focus {
			current = i
			break
		}
	}
	if current < 0 {
		return
	}
	height := max(1, m.height-len(m.routingHeader()))
	origin := targets[current]
	wanted := origin.start + direction*height
	best, bestDistance, bestColumnDistance := -1, 0, 0
	for i, target := range targets {
		if (target.start-origin.start)*direction <= 0 {
			continue
		}
		distance := max(target.start-wanted, wanted-target.start)
		columnDistance := max(target.column-origin.column, origin.column-target.column)
		if best < 0 || distance < bestDistance || (distance == bestDistance && columnDistance < bestColumnDistance) {
			best, bestDistance, bestColumnDistance = i, distance, columnDistance
		}
	}
	if best < 0 {
		best = current
	}
	selected := targets[best]
	m.focus = selected.focus
	m.scrollY = ui.EnsureLineVisible(m.scrollY+direction*height, height, len(lines), selected.start, selected.end)
}

// FocusID identifies a group header, its Locate button, or a candidate card.
type FocusID struct {
	Group string
	Node  string
	// Locate indicates focus on the group header's Locate button; Node is empty when true.
	Locate bool
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

// moveGroup treats Locate as part of its header rather than an extra vertical item.
func (m *Model) moveGroup(key string) {
	items := m.visibleItems()
	index := indexOfFocus(items, FocusID{Group: m.focus.Group})
	if key == "up" && index == 0 && m.routing.available {
		m.routing.focus = 1
		return
	}
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
