package protocol

// CapabilityProxyAuthPreview identifies read-only authentication draft previews.
const CapabilityProxyAuthPreview = "proxy-auth-preview-v1"

// ProxyAuthPreviewRequest resolves a draft without saving or applying it.
type ProxyAuthPreviewRequest struct {
	Config ProxyAuthConfig `json:"config"`
	Reveal bool            `json:"reveal,omitempty"`
}

// ProxyAuthPreviewLine is a YAML display line, optionally added or removed.
type ProxyAuthPreviewLine struct {
	Text   string `json:"text"`
	Field  string `json:"field"`
	Target string `json:"target,omitempty"`
	Change string `json:"change,omitempty"`
}

// ProxyAuthPreview contains only authentication YAML, never a full runtime config.
type ProxyAuthPreview struct {
	Revision uint64                 `json:"revision"`
	ActiveID string                 `json:"active_id,omitempty"`
	Lines    []ProxyAuthPreviewLine `json:"lines"`
}
