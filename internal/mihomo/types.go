package mihomo

import (
	"encoding/json"
	"time"
)

type Version struct {
	Meta    bool   `json:"meta"`
	Version string `json:"version"`
}

type Proxy struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	Now     string            `json:"now,omitempty"`
	All     []string          `json:"all,omitempty"`
	History []json.RawMessage `json:"history,omitempty"`
	UDP     bool              `json:"udp,omitempty"`
	XUDP    bool              `json:"xudp,omitempty"`
}

type Proxies struct {
	Proxies map[string]Proxy `json:"proxies"`
}

type Delays map[string]uint16

type Connection struct {
	ID       string             `json:"id"`
	Start    time.Time          `json:"start,omitempty"`
	Upload   int64              `json:"upload"`
	Download int64              `json:"download"`
	Chains   []string           `json:"chains,omitempty"`
	Rule     string             `json:"rule,omitempty"`
	RulePay  string             `json:"rulePayload,omitempty"`
	Metadata ConnectionMetadata `json:"metadata,omitempty"`
}

type ConnectionMetadata struct {
	Network           string `json:"network,omitempty"`
	Type              string `json:"type,omitempty"`
	SourceIP          string `json:"sourceIP,omitempty"`
	DestinationIP     string `json:"destinationIP,omitempty"`
	SourcePort        string `json:"sourcePort,omitempty"`
	DestinationPort   string `json:"destinationPort,omitempty"`
	Host              string `json:"host,omitempty"`
	Process           string `json:"process,omitempty"`
	ProcessPath       string `json:"processPath,omitempty"`
	InboundName       string `json:"inboundName,omitempty"`
	InboundUser       string `json:"inboundUser,omitempty"`
	SniffHost         string `json:"sniffHost,omitempty"`
	RemoteDestination string `json:"remoteDestination,omitempty"`
}

type Connections struct {
	DownloadTotal int64        `json:"downloadTotal"`
	UploadTotal   int64        `json:"uploadTotal"`
	Connections   []Connection `json:"connections"`
}

type Rule struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
	Proxy   string `json:"proxy"`
}

type Rules struct {
	Rules []Rule `json:"rules"`
}

type RuleProvider struct {
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	VehicleType string    `json:"vehicleType"`
	Behavior    string    `json:"behavior"`
	Format      string    `json:"format"`
	RuleCount   int       `json:"ruleCount"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type RuleProviders struct {
	Providers map[string]RuleProvider `json:"providers"`
}
