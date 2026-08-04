// Package zashboard implements the Zashboard panel adapter.
package zashboard

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/panel"
	"github.com/LeeShunEE/mihari/internal/panel/release"
)

const (
	owner = "Zephyruso"
	repo  = "zashboard"
)

// Adapter resolves Zashboard prebuilt release assets.
type Adapter struct {
	Client release.Client
}

var _ panel.Adapter = (*Adapter)(nil)

// New returns a Zashboard adapter using the given HTTP client and optional API base (tests).
func New(httpClient *http.Client, apiBase string) *Adapter {
	return &Adapter{Client: release.Client{HTTPClient: httpClient, APIBase: apiBase}}
}

func (a *Adapter) ID() string          { return panel.IDZashboard }
func (a *Adapter) DisplayName() string { return "Zashboard" }

// ResolveLatest prefers no-fonts / size-efficient dist zips when present; build id is the release tag.
func (a *Adapter) ResolveLatest(ctx context.Context) (string, string, error) {
	rel, err := a.Client.LatestRelease(ctx, owner, repo)
	if err != nil {
		return "", "", err
	}
	asset, err := SelectAsset(rel)
	if err != nil {
		return "", "", err
	}
	return rel.TagName, asset.URL, nil
}

// SetupPath returns a same-origin setup deep-link that points only at the gateway host.
// Zashboard-style configuration uses hostname (and optional port) without a secret.
func (a *Adapter) SetupPath(gatewayHost string) string {
	host := strings.TrimSpace(gatewayHost)
	if host == "" {
		host = "127.0.0.1:9191"
	}
	values := url.Values{}
	values.Set("hostname", host)
	// Disable core-upgrade UI surface when the panel honors the query (backend still rejects /upgrade).
	values.Set("disableUpgrade", "true")
	return "/?" + values.Encode()
}

// SelectAsset chooses a Zashboard dist zip, preferring no-fonts / smaller names.
func SelectAsset(rel release.Release) (release.Asset, error) {
	var preferred, fallback release.Asset
	var foundPreferred, foundFallback bool
	for _, asset := range rel.Assets {
		name := strings.ToLower(asset.Name)
		if !strings.HasSuffix(name, ".zip") {
			continue
		}
		// Skip source archives.
		if strings.Contains(name, "source") {
			continue
		}
		scorePreferred := strings.Contains(name, "no-font") ||
			strings.Contains(name, "nofont") ||
			strings.Contains(name, "without-font") ||
			strings.Contains(name, "dist-lite") ||
			strings.Contains(name, "cdn")
		isDist := strings.Contains(name, "dist") || strings.Contains(name, "zashboard") || scorePreferred
		if !isDist {
			continue
		}
		if scorePreferred {
			if !foundPreferred || len(asset.Name) < len(preferred.Name) {
				preferred = asset
				foundPreferred = true
			}
			continue
		}
		if !foundFallback {
			fallback = asset
			foundFallback = true
		}
	}
	if foundPreferred {
		return preferred, nil
	}
	if foundFallback {
		return fallback, nil
	}
	return release.Asset{}, protocol.APIError{
		Code:    protocol.CodeDataFailure,
		Message: "zashboard release has no compatible dist asset",
	}
}
