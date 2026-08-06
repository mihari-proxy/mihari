// Package zashboard implements the Zashboard panel adapter.
package zashboard

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/panel"
	"github.com/mihari-proxy/mihari/internal/panel/release"
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

// SetupPath returns a same-origin setup deep-link that points only at the gateway.
// Zashboard expects hostname and port as separate query params on /#/setup (no secret).
func (a *Adapter) SetupPath(gatewayHost string) string {
	host := strings.TrimSpace(gatewayHost)
	if host == "" {
		host = "127.0.0.1:9191"
	}
	hostname := host
	port := ""
	if i := strings.LastIndex(host, ":"); i >= 0 {
		// Bracketed IPv6: [::1]:9191 — keep hostname as [::1].
		if strings.HasPrefix(host, "[") {
			if end := strings.Index(host, "]"); end > 0 && end+1 < len(host) && host[end+1] == ':' {
				hostname = host[:end+1]
				port = host[end+2:]
			}
		} else if !strings.Contains(host[:i], ":") {
			hostname = host[:i]
			port = host[i+1:]
		}
	}
	if hostname == "" {
		hostname = "127.0.0.1"
	}
	values := url.Values{}
	values.Set("hostname", hostname)
	if port != "" {
		values.Set("port", port)
	}
	// Disable core-upgrade UI surface when the panel honors the query (backend still rejects /upgrade).
	values.Set("disableUpgrade", "true")
	return "/#/setup?" + values.Encode()
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
