package protocol

import (
	"encoding/json"
	"time"
)

type CoreStatus struct {
	Schema      string    `json:"schema"`
	Revision    uint64    `json:"revision"`
	Status      string    `json:"status"`
	Version     string    `json:"version,omitempty"`
	PID         int       `json:"pid,omitempty"`
	Restarts    uint64    `json:"restarts"`
	LastError   string    `json:"last_error,omitempty"`
	NextRetryAt time.Time `json:"next_retry_at,omitzero"`
}

type ProxyGroup struct {
	Name  string      `json:"name"`
	Type  string      `json:"type"`
	Now   string      `json:"now,omitempty"`
	All   []string    `json:"all,omitempty"`
	Nodes []ProxyNode `json:"nodes,omitempty"`
}

type ProxyNode struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	UDP  bool   `json:"udp,omitempty"`
	XUDP bool   `json:"xudp,omitempty"`
}

type ProxyGroups struct {
	Schema string       `json:"schema"`
	Groups []ProxyGroup `json:"groups"`
}

type ConnectionMetadata struct {
	Network           string `json:"network,omitempty"`
	Type              string `json:"type,omitempty"`
	SourceIP          string `json:"source_ip,omitempty"`
	DestinationIP     string `json:"destination_ip,omitempty"`
	SourcePort        string `json:"source_port,omitempty"`
	DestinationPort   string `json:"destination_port,omitempty"`
	Host              string `json:"host,omitempty"`
	Process           string `json:"process,omitempty"`
	ProcessPath       string `json:"process_path,omitempty"`
	InboundName       string `json:"inbound_name,omitempty"`
	InboundUser       string `json:"inbound_user,omitempty"`
	SniffHost         string `json:"sniff_host,omitempty"`
	RemoteDestination string `json:"remote_destination,omitempty"`
}

type Connection struct {
	ID            string             `json:"id"`
	Start         time.Time          `json:"start,omitzero"`
	ClosedAt      time.Time          `json:"closed_at,omitzero"`
	Upload        int64              `json:"upload"`
	Download      int64              `json:"download"`
	UploadSpeed   int64              `json:"upload_speed,omitempty"`
	DownloadSpeed int64              `json:"download_speed,omitempty"`
	Chains        []string           `json:"chains,omitempty"`
	Rule          string             `json:"rule,omitempty"`
	RulePay       string             `json:"rule_payload,omitempty"`
	Metadata      ConnectionMetadata `json:"metadata,omitzero"`
}

type ConnectionList struct {
	Schema        string       `json:"schema"`
	DownloadTotal int64        `json:"download_total"`
	UploadTotal   int64        `json:"upload_total"`
	Connections   []Connection `json:"connections"`
}

type Rule struct {
	Type    string `json:"type"`
	Payload string `json:"payload,omitempty"`
	Proxy   string `json:"proxy"`
}

type RuleList struct {
	Schema string `json:"schema"`
	Rules  []Rule `json:"rules"`
}

type DelayResult struct {
	Schema string            `json:"schema"`
	Delays map[string]uint16 `json:"delays"`
}

type MutationRequest struct {
	OperationID string  `json:"operation_id"`
	IfRevision  *uint64 `json:"if_revision,omitempty"`
}

type ProxySelectionRequest struct {
	OperationID string `json:"operation_id"`
	Name        string `json:"name"`
}

type DelayTestRequest struct {
	URL                 string `json:"url"`
	TimeoutMilliseconds int    `json:"timeout_ms"`
}

type MutationResult struct {
	Schema      string `json:"schema"`
	OperationID string `json:"operation_id"`
	Revision    uint64 `json:"revision,omitempty"`
}

type CoreInstallResult struct {
	Schema   string `json:"schema"`
	Version  string `json:"version"`
	Updated  bool   `json:"updated"`
	Revision uint64 `json:"revision"`
}

type StreamEvent struct {
	Schema     string          `json:"schema"`
	Stream     string          `json:"stream"`
	ObservedAt time.Time       `json:"observed_at,omitzero"`
	Data       json.RawMessage `json:"data"`
}

type TrafficSample struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

type MemorySample struct {
	InUse   int64 `json:"inuse"`
	OSLimit int64 `json:"oslimit,omitempty"`
}

type LogEntry struct {
	Level   string `json:"type"`
	Message string `json:"payload"`
}
