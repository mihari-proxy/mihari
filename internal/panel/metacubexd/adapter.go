// Package metacubexd implements the MetaCubeXD panel adapter.
package metacubexd

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/mihari-proxy/mihari/internal/panel"
	"github.com/mihari-proxy/mihari/internal/panel/release"
)

const (
	owner  = "MetaCubeX"
	repo   = "metacubexd"
	branch = "gh-pages"
)

// Adapter resolves MetaCubeXD gh-pages static trees keyed by commit SHA.
type Adapter struct {
	Client release.Client
}

var _ panel.Adapter = (*Adapter)(nil)

// New returns a MetaCubeXD adapter using the given HTTP client and optional API base (tests).
func New(httpClient *http.Client, apiBase string) *Adapter {
	return &Adapter{Client: release.Client{HTTPClient: httpClient, APIBase: apiBase}}
}

func (a *Adapter) ID() string          { return panel.IDMetaCubeXD }
func (a *Adapter) DisplayName() string { return "MetaCubeXD" }

// ResolveLatest uses the gh-pages branch tip SHA as the immutable build id.
func (a *Adapter) ResolveLatest(ctx context.Context) (string, string, error) {
	sha, err := a.Client.BranchTip(ctx, owner, repo, branch)
	if err != nil {
		return "", "", err
	}
	// Prefer short immutable id for directory names while remaining unique enough for rollback labels.
	build := sha
	if len(sha) > 12 {
		build = sha[:12]
	}
	assetURL := release.ArchiveURL(a.Client.APIBase, owner, repo, branch)
	// When using a real GitHub API base, point downloads at the commit zipball for immutability.
	if a.Client.APIBase == "" || strings.Contains(a.Client.APIBase, "api.github.com") {
		assetURL = "https://github.com/" + owner + "/" + repo + "/archive/" + sha + ".zip"
	} else {
		assetURL = strings.TrimRight(a.Client.APIBase, "/") + "/repos/" + owner + "/" + repo + "/zipball/" + sha
	}
	return build, assetURL, nil
}

// SetupPath returns a same-origin deep-link that configures backend host without secrets.
func (a *Adapter) SetupPath(gatewayHost string) string {
	host := strings.TrimSpace(gatewayHost)
	if host == "" {
		host = "127.0.0.1:9191"
	}
	values := url.Values{}
	values.Set("hostname", host)
	values.Set("http", "true")
	return "/#/setup?" + values.Encode()
}
