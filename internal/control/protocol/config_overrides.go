package protocol

// MaxControlRequestBytes bounds the encoded JSON sent to a local mutation or preview.
const MaxControlRequestBytes = 1 << 20

// CapabilityConfigOverrides identifies instance overrides and read-only draft previews.
const CapabilityConfigOverrides = "config-overrides-v1"

// OverrideFragment is one ordered, independently enabled YAML input.
type OverrideFragment struct {
	ID      string `json:"id" yaml:"id"`
	Name    string `json:"name" yaml:"name"`
	Enabled bool   `json:"enabled" yaml:"enabled"`
	YAML    string `json:"yaml" yaml:"yaml"`
}

// DNSFieldOverride describes an explicit operation on a structured DNS field.
type DNSFieldOverride struct {
	Operation string   `json:"operation" yaml:"operation"`
	Bool      *bool    `json:"bool,omitempty" yaml:"bool,omitempty"`
	Mode      string   `json:"mode,omitempty" yaml:"mode,omitempty"`
	Servers   []string `json:"servers,omitempty" yaml:"servers,omitempty"`
}

// ConfigOverrides holds instance DNS choices and the ordered original fragment text.
type ConfigOverrides struct {
	DNS       map[string]DNSFieldOverride `json:"dns,omitempty" yaml:"dns,omitempty"`
	Fragments []OverrideFragment          `json:"fragments,omitempty" yaml:"fragments,omitempty"`
}

// ConfigOverridesStatus describes the editable snapshot, including disabled fragments.
type ConfigOverridesStatus struct {
	WarningOutcome
	Schema     string          `json:"schema"`
	Revision   uint64          `json:"revision"`
	ActiveID   string          `json:"active_id,omitempty"`
	ActiveName string          `json:"active_name,omitempty"`
	Config     ConfigOverrides `json:"config"`
}

// ConfigOverridesUpdateRequest replaces the selected category at a revision.
type ConfigOverridesUpdateRequest struct {
	OperationID string                      `json:"operation_id"`
	IfRevision  *uint64                     `json:"if_revision,omitempty"`
	DNS         map[string]DNSFieldOverride `json:"dns"`
	Fragments   []OverrideFragment          `json:"fragments"`
}

// ConfigOverridesPreviewRequest resolves a selected editor's draft against a single source.
type ConfigOverridesPreviewRequest struct {
	Category       string                      `json:"category"`
	DNS            map[string]DNSFieldOverride `json:"dns,omitempty"`
	Fragments      []OverrideFragment          `json:"fragments,omitempty"`
	Authentication *ProxyAuthConfig            `json:"authentication,omitempty"`
	Full           bool                        `json:"full,omitempty"`
	Reveal         bool                        `json:"reveal,omitempty"`
	Offset         int                         `json:"offset,omitempty"`
	Snapshot       string                      `json:"snapshot,omitempty"`
}

// OverridePreviewLine is a display segment of candidate YAML or a removed line.
type OverridePreviewLine struct {
	Target    string `json:"target,omitempty"`
	Text      string `json:"text"`
	Field     string `json:"field,omitempty"`
	Source    string `json:"source,omitempty"`
	Change    string `json:"change,omitempty"`
	Continued bool   `json:"continued,omitempty"`
}

// ConfigOverridesPreview is a bounded page of a draft's effective configuration.
type ConfigOverridesPreview struct {
	Revision   uint64                `json:"revision"`
	ActiveID   string                `json:"active_id,omitempty"`
	ActiveName string                `json:"active_name,omitempty"`
	Snapshot   string                `json:"snapshot"`
	NextOffset int                   `json:"next_offset,omitempty"`
	Lines      []OverridePreviewLine `json:"lines"`
}
