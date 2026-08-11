package protocol

import "time"

type Subscription struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Enabled     bool      `json:"enabled"`
	AutoRefresh bool      `json:"auto_refresh"`
	Interval    string    `json:"interval"`
	Cached      bool      `json:"cached"`
	Generation  uint64    `json:"generation"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	// Traffic quota from provider subscription-userinfo (bytes).
	Upload   int64 `json:"upload,omitempty"`
	Download int64 `json:"download,omitempty"`
	Total    int64 `json:"total,omitempty"`
	Expire   int64 `json:"expire,omitempty"`
	// ProxyMode is the per-subscription refresh transport: direct (omitted), proxy, or auto.
	ProxyMode string `json:"proxy_mode,omitempty"`
}

type SubscriptionList struct {
	Schema         string         `json:"schema"`
	Revision       uint64         `json:"revision"`
	ActiveID       string         `json:"active_id,omitempty"`
	GlobalInterval string         `json:"global_interval"`
	Subscriptions  []Subscription `json:"subscriptions"`
}

type SubscriptionResult struct {
	Schema       string       `json:"schema"`
	OperationID  string       `json:"operation_id,omitempty"`
	Revision     uint64       `json:"revision"`
	Subscription Subscription `json:"subscription"`
}

type SubscriptionAddRequest struct {
	OperationID string  `json:"operation_id"`
	IfRevision  *uint64 `json:"if_revision,omitempty"`
	Name        string  `json:"name"`
	URL         string  `json:"url"`
	ProxyMode   string  `json:"proxy_mode,omitempty"`
}

type SubscriptionEnabledRequest struct {
	OperationID string  `json:"operation_id"`
	IfRevision  *uint64 `json:"if_revision,omitempty"`
	Enabled     bool    `json:"enabled"`
}

type SubscriptionUpdateRequest struct {
	OperationID    string  `json:"operation_id"`
	IfRevision     *uint64 `json:"if_revision,omitempty"`
	Name           *string `json:"name,omitempty"`
	URL            *string `json:"url,omitempty"`
	Interval       *string `json:"interval,omitempty"`
	AutoRefresh    *bool   `json:"auto_refresh,omitempty"`
	GlobalInterval *string `json:"global_interval,omitempty"`
	ProxyMode      *string `json:"proxy_mode,omitempty"`
}
