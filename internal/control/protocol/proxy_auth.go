package protocol

// CapabilityProxyAuth identifies instance proxy authentication management.
const CapabilityProxyAuth = "proxy-auth-v1"

// ProxyAuthUser is a configured proxy credential. Passwords only appear in explicit reads.
type ProxyAuthUser struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Source   string `json:"source,omitempty"`
}

// ProxyAuthConfig is the saved instance override; follow leaves subscription users unchanged.
type ProxyAuthConfig struct {
	Mode           string          `json:"mode"`
	Users          []ProxyAuthUser `json:"users"`
	BypassMode     string          `json:"bypass_mode"`
	LocalBypass    bool            `json:"local_bypass"`
	BypassPrefixes []string        `json:"bypass_prefixes"`
}

// ProxyAuthStatus separates saved overrides from effective subscription users.
type ProxyAuthStatus struct {
	Problem string `json:"problem,omitempty"`
	WarningOutcome
	Schema         string          `json:"schema"`
	Revision       uint64          `json:"revision"`
	Config         ProxyAuthConfig `json:"config"`
	Users          []ProxyAuthUser `json:"users"`
	BypassPrefixes []string        `json:"bypass_prefixes"`
}

// ProxyAuthUpdateRequest replaces saved overrides at the supplied revision.
type ProxyAuthUpdateRequest struct {
	OperationID string          `json:"operation_id"`
	IfRevision  *uint64         `json:"if_revision,omitempty"`
	Config      ProxyAuthConfig `json:"config"`
	Reset       bool            `json:"reset,omitempty"`
}
