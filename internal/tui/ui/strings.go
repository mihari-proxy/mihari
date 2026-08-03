package ui

const (
	AppName                  = "Mihari"
	Connecting               = "Connecting to the Mihari daemon…"
	ResizeRequired           = "Terminal too small"
	ResizeInstructions       = "Resize to at least 72×22. Press ? for help or q to quit."
	UnavailableTitle         = "Unavailable"
	UnavailableReason        = "This page is waiting for daemon capability support."
	FooterRail               = "↑/↓ page  Enter/→ open  ? help  q quit"
	FooterContent            = "← navigation  ? help  q quit"
	HelpTitle                = "Keyboard help"
	HelpBody                 = "Navigation: ↑/↓ select a page, Enter or → enters content, ← returns when available.\nTab is reserved for forms and dialogs. Press q to quit."
	ConfirmLabel             = "Confirm"
	CancelLabel              = "Cancel"
	ObjectLabel              = "Object"
	ImpactLabel              = "Impact"
	RollbackLabel            = "Rollback"
	StaleLabel               = "Stale"
	MonitorTrafficTitle      = "Traffic · 60 s"
	MonitorMemoryTitle       = "Memory · 60 s"
	MonitorConnectionsLabel  = "Connections"
	MonitorMemoryLabel       = "Memory"
	MonitorMemoryShort       = "MEM"
	MonitorUploadShort       = "UL"
	MonitorDownloadShort     = "DL"
	MonitorUploadTotal       = "Uploaded"
	MonitorDownloadTotal     = "Downloaded"
	UnknownLabel             = "Unknown"
	AvailableLabel           = "Available"
	PIDLabel                 = "PID"
	DesiredLabel             = "Desired"
	ObservedLabel            = "Observed"
	CacheMissingLabel        = "Cache missing"
	NoRecentOperations       = "No operations in this session"
	CoreCardTitle            = "Core"
	ConfigCardTitle          = "Config"
	SubscriptionCardTitle    = "Subscription"
	WebGUICardTitle          = "Web GUI"
	RecentOperationsTitle    = "Recent operations"
	NoProxyGroups            = "No proxy groups are available."
	MissingValue             = "—"
	TestingLabel             = "Testing…"
	TimeoutLabel             = "Timeout"
	ConnectionsActiveLabel   = "Active"
	ConnectionsClosedLabel   = "Closed"
	SourceIPLabel            = "Source IP"
	SearchPlaceholder        = "Search…"
	ColumnsLabel             = "Columns"
	PauseLabel               = "Pause"
	ResumeLabel              = "Resume"
	ConnectionDetailsTitle   = "Connection details"
	OverviewTabLabel         = "Overview"
	RawTabLabel              = "Raw"
	ProxiesTabLabel          = "Proxies"
	BasicSectionTitle        = "Basic"
	SourceDestinationTitle   = "Source & destination"
	TrafficSectionTitle      = "Traffic"
	OutboundSectionTitle     = "Outbound"
	ChainLabel               = "Chain"
	NoConnections            = "No matching connections."
	ColumnPickerTitle        = "Visible columns"
	SaveLabel                = "Save"
	CloseAllConnectionsTitle = "Close all connections"
	AllActiveConnections     = "All active connections"
	CloseAllImpact           = "Every current connection will be interrupted."
	CloseAllRollback         = "Closed connections cannot be restored."
)

var connectionColumnLabels = map[string]string{
	"host": "Host", "network": "Network", "source": "Source", "destination": "Destination",
	"chain": "Chain", "rule": "Rule", "process": "Process", "upload": "Upload",
	"download": "Download", "traffic": "Traffic", "start": "Start",
}

func ConnectionColumnLabel(id string) string {
	if label := connectionColumnLabels[id]; label != "" {
		return label
	}
	return id
}

var pageLabels = map[PageID]string{
	PageOverview:      "Overview",
	PageProxies:       "Proxies",
	PageConnections:   "Connections",
	PageRules:         "Rules",
	PageLogs:          "Logs",
	PageSubscriptions: "Subscriptions",
	PageWebGUI:        "Web GUI",
	PageSystem:        "System",
	PageSetup:         "Setup",
}

func PageLabel(id PageID) string {
	if label := pageLabels[id]; label != "" {
		return label
	}
	return string(id)
}
