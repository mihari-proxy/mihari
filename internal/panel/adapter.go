package panel

import "context"

// Adapter resolves distribution assets and same-origin setup deep-links for a built-in panel.
type Adapter interface {
	ID() string
	DisplayName() string
	// ResolveLatest returns build id and download URL using an injected HTTP client.
	ResolveLatest(ctx context.Context) (build string, assetURL string, err error)
	// SetupPath returns a same-origin setup deep-link path+query (no secret, no controller host).
	SetupPath(gatewayHost string) string
}
