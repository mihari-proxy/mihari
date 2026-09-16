package protocol

import "time"

type Subscription struct {
	// CacheOutdated reports a cache fetched from a different current URL.
	CacheOutdated bool `json:"cache_outdated,omitempty"`
	// ScheduleFrom overrides cache age as the next refresh scheduling origin.
	ScheduleFrom time.Time `json:"schedule_from,omitzero"`
	// IntervalRefreshRequired forces expiry until a successful refresh.
	IntervalRefreshRequired bool      `json:"interval_refresh_required,omitempty"`
	ID                      string    `json:"id"`
	Name                    string    `json:"name"`
	Enabled                 bool      `json:"enabled"`
	AutoRefresh             bool      `json:"auto_refresh"`
	Interval                string    `json:"interval"`
	Cached                  bool      `json:"cached"`
	Generation              uint64    `json:"generation"`
	UpdatedAt               time.Time `json:"updated_at,omitempty"`
	LastError               string    `json:"last_error,omitempty"`
	// Traffic quota from provider subscription-userinfo (bytes).
	Upload   int64 `json:"upload,omitempty"`
	Download int64 `json:"download,omitempty"`
	Total    int64 `json:"total,omitempty"`
	Expire   int64 `json:"expire,omitempty"`
	// ProxyMode is the per-subscription refresh transport: direct (omitted), proxy, or auto.
	ProxyMode string `json:"proxy_mode,omitempty"`
}

// SubscriptionURL is the authenticated, explicit current-source reveal response.
type SubscriptionURL struct {
	// Schema identifies the local control protocol version.
	Schema string `json:"schema"`
	// URL is the complete current subscription source.
	URL string `json:"url"`
}

type SubscriptionList struct {
	WarningOutcome
	Schema         string         `json:"schema"`
	Revision       uint64         `json:"revision"`
	ActiveID       string         `json:"active_id,omitempty"`
	GlobalInterval string         `json:"global_interval"`
	Subscriptions  []Subscription `json:"subscriptions"`
}

type SubscriptionResult struct {
	WarningOutcome
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
