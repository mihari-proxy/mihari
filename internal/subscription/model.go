package subscription

import "time"

const CatalogSchema = "mihari.subscriptions/v1"

type Profile struct {
	ID           string    `yaml:"id"`
	Name         string    `yaml:"name"`
	URL          string    `yaml:"url"`
	Enabled      bool      `yaml:"enabled"`
	AutoRefresh  bool      `yaml:"auto-refresh"`
	Interval     string    `yaml:"interval,omitempty"`
	Version      uint64    `yaml:"version,omitempty"`
	Generation   uint64    `yaml:"generation,omitempty"`
	UpdatedAt    time.Time `yaml:"updated-at,omitempty"`
	ETag         string    `yaml:"etag,omitempty"`
	LastModified string    `yaml:"last-modified,omitempty"`
	LastError    string    `yaml:"last-error,omitempty"`
	// Traffic quota from subscription-userinfo (bytes). Zero means unknown.
	Upload   int64 `yaml:"upload,omitempty"`
	Download int64 `yaml:"download,omitempty"`
	Total    int64 `yaml:"total,omitempty"`
	Expire   int64 `yaml:"expire,omitempty"` // unix seconds
}

type Catalog struct {
	Schema         string    `yaml:"schema"`
	GlobalInterval string    `yaml:"global-interval"`
	ActiveID       string    `yaml:"active-id,omitempty"`
	Profiles       []Profile `yaml:"profiles,omitempty"`
}

type PublicProfile struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Enabled     bool      `json:"enabled"`
	AutoRefresh bool      `json:"auto_refresh"`
	Interval    string    `json:"interval"`
	Cached      bool      `json:"cached"`
	Generation  uint64    `json:"generation"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	// Traffic from subscription-userinfo (bytes). Omitted when unknown.
	Upload   int64 `json:"upload,omitempty"`
	Download int64 `json:"download,omitempty"`
	Total    int64 `json:"total,omitempty"`
	Expire   int64 `json:"expire,omitempty"`
}

type PublicCatalog struct {
	ActiveID       string          `json:"active_id,omitempty"`
	GlobalInterval string          `json:"global_interval"`
	Profiles       []PublicProfile `json:"profiles"`
}

func (c Catalog) Public() PublicCatalog {
	result := PublicCatalog{ActiveID: c.ActiveID, GlobalInterval: c.GlobalInterval, Profiles: make([]PublicProfile, 0, len(c.Profiles))}
	for _, profile := range c.Profiles {
		result.Profiles = append(result.Profiles, PublicProfile{
			ID: profile.ID, Name: profile.Name, Enabled: profile.Enabled, AutoRefresh: profile.AutoRefresh,
			Interval: profile.Interval, Cached: profile.Generation > 0, Generation: profile.Generation,
			UpdatedAt: profile.UpdatedAt, LastError: profile.LastError,
			Upload: profile.Upload, Download: profile.Download, Total: profile.Total, Expire: profile.Expire,
		})
	}
	return result
}

func (c Catalog) Clone() Catalog {
	c.Profiles = append([]Profile(nil), c.Profiles...)
	return c
}
