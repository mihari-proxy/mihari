package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

const maxReleaseResponseSize = 2 << 20

type Asset struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"browser_download_url"`
	Size      int64  `json:"size"`
	Digest    string `json:"digest"`
	State     string `json:"state"`
	UpdatedAt string `json:"updated_at"`
}

type Release struct {
	ID      int64   `json:"id"`
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// LatestRelease 取 mihomo 最新 release（bundler 复用入口，design §4.1 export 边界）。
// channel=="alpha" 走滚动 tag Prerelease-Alpha；其余（含空）走 /releases/latest。
func (i Installer) LatestRelease(ctx context.Context, channel string) (release Release, resultErr error) {
	path := "/releases/latest"
	if channel == "alpha" {
		path = "/releases/tags/Prerelease-Alpha"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(i.apiBase(), "/")+"/repos/"+i.repository()+path, nil)
	if err != nil {
		return release, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInternal, Message: "create release request"}, err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	request.Header.Set("User-Agent", "mihari")
	response, err := i.httpClient().Do(request)
	if err != nil {
		return release, coreHTTPError(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "fetch mihomo release failed"}, "core GET release", request.URL.String(), "transport", nil, err)
	}
	defer closeCoreResponse(ctx, response, "core GET release", request.URL.String(), &resultErr, i.Reporter)
	if response.StatusCode != http.StatusOK {
		return release, coreHTTPError(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "fetch mihomo release failed", Details: map[string]any{"status": response.StatusCode}}, "core GET release", request.URL.String(), "response", response, nil)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxReleaseResponseSize+1))
	if err != nil {
		return release, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "read mihomo release failed"}, &diagnostics.HTTPError{Operation: "core GET release", URL: request.URL.String(), Phase: "read", Status: response.StatusCode, Cause: err})
	}
	if len(raw) > maxReleaseResponseSize {
		return release, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo release response is too large"}
	}
	if err := json.Unmarshal(raw, &release); err != nil {
		return Release{}, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid mihomo release response"}, errors.Join(err, commandOutputCause("github release response", raw)))
	}
	if release.TagName == "" {
		return Release{}, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid mihomo release response"}, errors.New("github release response is missing tag_name"))
	}
	return release, nil
}

func SelectAsset(release Release, goos, goarch, channel string) (Asset, error) {
	if (goos != "windows" && goos != "linux" && goos != "darwin") || (goarch != "amd64" && goarch != "arm64") {
		return Asset{}, dataFailure("unsupported mihomo platform")
	}
	if channel != "" && channel != "stable" && channel != "alpha" {
		return Asset{}, dataFailure("unsupported mihomo channel")
	}
	extension := ".gz"
	if goos == "windows" {
		extension = ".zip"
	}
	if channel == "alpha" {
		return selectAlphaAsset(release, goos, goarch, extension)
	}
	prefix := "mihomo-" + goos + "-" + goarch
	bestScore := -1
	var best Asset
	ambiguous := false
	for _, asset := range release.Assets {
		score := -1
		suffix := "-" + release.TagName + extension
		if asset.Name == prefix+suffix {
			score = 10
		} else if goarch == "amd64" && asset.Name == prefix+"-compatible"+suffix {
			score = 15
		} else if variant, ok := strings.CutPrefix(asset.Name, prefix+"-go"); ok {
			if version, ok := strings.CutSuffix(variant, suffix); ok && decimalVersion(version) {
				score = 5
			}
		}
		if score < 0 {
			continue
		}
		if score > bestScore {
			bestScore = score
			best = asset
			ambiguous = false
		} else if score == bestScore {
			ambiguous = true
		}
	}
	if bestScore < 0 {
		return Asset{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo release has no compatible asset"}
	}
	if ambiguous {
		return Asset{}, dataFailure("mihomo release has ambiguous compatible assets")
	}
	return best, nil
}

func decimalVersion(version string) bool {
	if version == "" {
		return false
	}
	for _, digit := range version {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func selectAlphaAsset(release Release, goos, goarch, extension string) (Asset, error) {
	prefix := "mihomo-" + strings.ToLower(goos) + "-"
	archToken := strings.ToLower(goarch)
	var selected Asset
	found := false
	for _, asset := range release.Assets {
		if _, ok := parseAlphaAsset(asset.Name); !ok {
			continue
		}
		name := strings.ToLower(asset.Name)
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, extension) {
			continue
		}
		if !strings.HasPrefix(name, prefix+archToken+"-") {
			continue
		}
		if found {
			return Asset{}, dataFailure("mihomo release has ambiguous compatible assets")
		}
		selected, found = asset, true
	}
	if found {
		return selected, nil
	}
	return Asset{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo release has no compatible asset"}
}
