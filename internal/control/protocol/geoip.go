package protocol

import "time"

// GeoIPDatabaseStatus is the stable health DTO for one local MMDB file.
type GeoIPDatabaseStatus struct {
	Available bool      `json:"available"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	Error     string    `json:"error,omitempty"`
}

// GeoIPStatus reports daemon-owned Country and ASN database health.
type GeoIPStatus struct {
	Schema   string              `json:"schema"`
	Revision uint64              `json:"revision"`
	Country  GeoIPDatabaseStatus `json:"country"`
	ASN      GeoIPDatabaseStatus `json:"asn"`
}

// GeoIPLookupRequest contains a bounded set of unique public addresses.
type GeoIPLookupRequest struct {
	Addresses []string `json:"addresses"`
}

// GeoIPRecord contains safe local geolocation data for one address.
type GeoIPRecord struct {
	Address      string `json:"address"`
	CountryCode  string `json:"country_code,omitempty"`
	ASN          uint32 `json:"asn,omitempty"`
	Organization string `json:"organization,omitempty"`
}

// GeoIPLookupResult is the versioned batch lookup response.
type GeoIPLookupResult struct {
	Schema  string        `json:"schema"`
	Records []GeoIPRecord `json:"records"`
}

// GeoIPUpdateResult reports a coordinated database-pair update.
type GeoIPUpdateResult struct {
	Schema      string      `json:"schema"`
	OperationID string      `json:"operation_id"`
	Revision    uint64      `json:"revision"`
	Status      GeoIPStatus `json:"status"`
}
