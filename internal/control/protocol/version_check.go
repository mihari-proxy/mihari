package protocol

// VersionCheck reports upstream metadata without downloading or installing assets.
// Channel is set for core checks; panel build identities may be commit hashes.
type VersionCheck struct {
	Schema  string `json:"schema"`
	Latest  string `json:"latest"`
	Channel string `json:"channel,omitempty"`
}
