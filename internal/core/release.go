package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

const maxReleaseResponseSize = 2 << 20

type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

func (i Installer) latestRelease(ctx context.Context) (Release, error) {
	var release Release
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(i.apiBase(), "/")+"/repos/"+i.repository()+"/releases/latest", nil)
	if err != nil {
		return release, protocol.APIError{Code: protocol.CodeInternal, Message: "create release request"}
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	request.Header.Set("User-Agent", "mihari")
	response, err := i.httpClient().Do(request)
	if err != nil {
		return release, protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "fetch mihomo release failed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return release, protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "fetch mihomo release failed", Details: map[string]any{"status": response.StatusCode}}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxReleaseResponseSize+1))
	if err != nil {
		return release, protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "read mihomo release failed"}
	}
	if len(raw) > maxReleaseResponseSize {
		return release, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo release response is too large"}
	}
	if err := json.Unmarshal(raw, &release); err != nil || release.TagName == "" {
		return Release{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid mihomo release response"}
	}
	return release, nil
}

func SelectAsset(release Release, goos, goarch string) (Asset, error) {
	extension := ".gz"
	if goos == "windows" {
		extension = ".zip"
	}
	prefix := "mihomo-" + strings.ToLower(goos) + "-"
	archToken := strings.ToLower(goarch)
	bestScore := -1
	var best Asset
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, extension) || !strings.Contains(name, "-"+archToken) {
			continue
		}
		if goarch == "arm64" && strings.Contains(name, "armv") {
			continue
		}
		score := 10
		if goarch == "amd64" && strings.Contains(name, "-compatible") {
			score += 5
		}
		if strings.Contains(name, strings.ToLower(release.TagName)) {
			score += 3
		}
		if score > bestScore {
			bestScore = score
			best = asset
		}
	}
	if bestScore < 0 {
		return Asset{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo release has no compatible asset"}
	}
	return best, nil
}
