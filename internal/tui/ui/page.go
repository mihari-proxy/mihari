package ui

import tea "charm.land/bubbletea/v2"

type PageID string

const (
	PageOverview      PageID = "overview"
	PageProxies       PageID = "proxies"
	PageConnections   PageID = "connections"
	PageRules         PageID = "rules"
	PageLogs          PageID = "logs"
	PageSubscriptions PageID = "subscriptions"
	PageWebGUI        PageID = "web-gui"
	PageSystem        PageID = "system"
	PageSetup         PageID = "setup"
)

var railPages = []PageID{
	PageOverview,
	PageProxies,
	PageConnections,
	PageRules,
	PageLogs,
	PageSubscriptions,
	PageWebGUI,
	PageSystem,
}

func RailPages() []PageID { return append([]PageID(nil), railPages...) }

type Page interface {
	ID() PageID
	SetSize(width, height int)
	FocusFirst()
	Update(tea.Msg) (Page, tea.Cmd)
	View() string
}
