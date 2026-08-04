// Package release provides small GitHub API helpers for panel distribution discovery.
package release

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

const maxResponseSize = 2 << 20

// Asset is a GitHub release asset.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is a GitHub release document.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Branch is a GitHub branch tip document.
type Branch struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// Client fetches release and branch metadata with bounded responses.
type Client struct {
	HTTPClient *http.Client
	APIBase    string
}

func (c Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c Client) apiBase() string {
	if c.APIBase != "" {
		return strings.TrimRight(c.APIBase, "/")
	}
	return "https://api.github.com"
}

// LatestRelease returns the latest release for owner/repo.
func (c Client) LatestRelease(ctx context.Context, owner, repo string) (Release, error) {
	var release Release
	url := c.apiBase() + "/repos/" + owner + "/" + repo + "/releases/latest"
	if err := c.getJSON(ctx, url, &release); err != nil {
		return Release{}, err
	}
	if release.TagName == "" {
		return Release{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid github release response"}
	}
	return release, nil
}

// BranchTip returns the tip commit SHA for owner/repo@branch.
func (c Client) BranchTip(ctx context.Context, owner, repo, branch string) (string, error) {
	var tip Branch
	url := c.apiBase() + "/repos/" + owner + "/" + repo + "/branches/" + branch
	if err := c.getJSON(ctx, url, &tip); err != nil {
		return "", err
	}
	if tip.Commit.SHA == "" {
		return "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid github branch response"}
	}
	return tip.Commit.SHA, nil
}

// ArchiveURL returns the GitHub archive download URL for a ref (tag, branch, or SHA).
func ArchiveURL(apiBase, owner, repo, ref string) string {
	base := strings.TrimRight(apiBase, "/")
	if base == "" || strings.HasPrefix(base, "https://api.github.com") {
		return "https://github.com/" + owner + "/" + repo + "/archive/refs/heads/" + ref + ".zip"
	}
	// Test servers expose archive under the same origin.
	return base + "/repos/" + owner + "/" + repo + "/zipball/" + ref
}

func (c Client) getJSON(ctx context.Context, url string, dest any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return protocol.APIError{Code: protocol.CodeInternal, Message: "create github request"}
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "mihari")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "fetch github resource failed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return protocol.APIError{
			Code:    protocol.CodeNetworkFailure,
			Message: "fetch github resource failed",
			Details: map[string]any{"status": response.StatusCode},
		}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "read github resource failed"}
	}
	if len(raw) > maxResponseSize {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "github response is too large"}
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid github response"}
	}
	return nil
}
