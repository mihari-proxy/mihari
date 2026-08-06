// Package update downloads and replaces the Mihari binary (self-update).
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const (
	maxReleaseResponseSize = 2 << 20
	maxSelfBinarySize      = 128 << 20
	defaultRepo            = "mihari-proxy/mihari"
)

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

// SelfUpdater installs a newer Mihari binary from GitHub Releases.
type SelfUpdater struct {
	HTTPClient *http.Client
	APIBase    string
	Repository string
	GOOS       string
	GOARCH     string
	// AfterReplace is optional; used to restart the OS service after a successful replace.
	AfterReplace func(ctx context.Context, version string) error
}

// Result describes a self-update attempt.
type Result struct {
	Version string
	Updated bool
}

func (u SelfUpdater) httpClient() *http.Client {
	if u.HTTPClient != nil {
		return u.HTTPClient
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

func (u SelfUpdater) apiBase() string {
	if u.APIBase != "" {
		return strings.TrimRight(u.APIBase, "/")
	}
	return "https://api.github.com"
}

func (u SelfUpdater) repository() string {
	if u.Repository != "" {
		return u.Repository
	}
	return defaultRepo
}

func (u SelfUpdater) targetOS() string {
	if u.GOOS != "" {
		return u.GOOS
	}
	return runtime.GOOS
}

func (u SelfUpdater) targetArch() string {
	if u.GOARCH != "" {
		return u.GOARCH
	}
	return runtime.GOARCH
}

// Update downloads the latest release when newer than currentVersion and replaces binaryPath.
func (u SelfUpdater) Update(ctx context.Context, binaryPath, currentVersion string) (Result, error) {
	release, err := u.latestRelease(ctx)
	if err != nil {
		return Result{}, err
	}
	if sameTag(currentVersion, release.TagName) {
		return Result{Version: release.TagName, Updated: false}, nil
	}
	asset, err := SelectSelfAsset(release, u.targetOS(), u.targetArch())
	if err != nil {
		return Result{}, err
	}
	if asset.Size < 0 || asset.Size > maxSelfBinarySize {
		return Result{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihari asset is too large"}
	}
	stagingDir := filepath.Join(filepath.Dir(binaryPath), ".mihari-update")
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create self-update staging: %w", err)
	}
	defer os.RemoveAll(stagingDir)

	candidate := filepath.Join(stagingDir, filepath.Base(binaryPath)+".new")
	if err := u.download(ctx, asset.URL, candidate); err != nil {
		return Result{}, err
	}
	if err := os.Chmod(candidate, 0o755); err != nil {
		return Result{}, err
	}
	if err := replaceBinary(candidate, binaryPath); err != nil {
		return Result{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "replace mihari binary"}
	}
	if u.AfterReplace != nil {
		if err := u.AfterReplace(ctx, release.TagName); err != nil {
			return Result{Version: release.TagName, Updated: true}, err
		}
	}
	return Result{Version: release.TagName, Updated: true}, nil
}

func sameTag(current, latest string) bool {
	a := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(current)), "v")
	b := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(latest)), "v")
	return a != "" && a == b
}

// SelectSelfAsset picks a mihari binary asset for goos/goarch.
// Expected names include mihari-windows-amd64.exe, mihari-linux-arm64, etc.
func SelectSelfAsset(release Release, goos, goarch string) (Asset, error) {
	goos = strings.ToLower(goos)
	goarch = strings.ToLower(goarch)
	wantSuffix := ""
	if goos == "windows" {
		wantSuffix = ".exe"
	}
	prefix := "mihari-" + goos + "-" + goarch
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if wantSuffix != "" && !strings.HasSuffix(name, wantSuffix) {
			continue
		}
		if wantSuffix == "" && strings.HasSuffix(name, ".exe") {
			continue
		}
		// Prefer exact binary over archives for simplicity in Phase 6.
		if strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".gz") {
			continue
		}
		return asset, nil
	}
	return Asset{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihari release has no compatible asset"}
}

func (u SelfUpdater) latestRelease(ctx context.Context) (Release, error) {
	var release Release
	url := u.apiBase() + "/repos/" + u.repository() + "/releases/latest"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return release, protocol.APIError{Code: protocol.CodeInternal, Message: "create release request"}
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "mihari")
	response, err := u.httpClient().Do(request)
	if err != nil {
		return release, protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "fetch mihari release failed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return release, protocol.APIError{
			Code: protocol.CodeNetworkFailure, Message: "fetch mihari release failed",
			Details: map[string]any{"status": response.StatusCode},
		}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxReleaseResponseSize+1))
	if err != nil {
		return release, protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "read mihari release failed"}
	}
	if len(raw) > maxReleaseResponseSize {
		return release, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihari release response is too large"}
	}
	if err := json.Unmarshal(raw, &release); err != nil || release.TagName == "" {
		return Release{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid mihari release response"}
	}
	return release, nil
}

func (u SelfUpdater) download(ctx context.Context, assetURL, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return protocol.APIError{Code: protocol.CodeInternal, Message: "create mihari download request"}
	}
	request.Header.Set("User-Agent", "mihari")
	response, err := u.httpClient().Do(request)
	if err != nil {
		return protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download mihari asset failed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return protocol.APIError{
			Code: protocol.CodeNetworkFailure, Message: "download mihari asset failed",
			Details: map[string]any{"status": response.StatusCode},
		}
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create mihari candidate: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, maxSelfBinarySize+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(destination)
		return protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "read mihari asset failed"}
	}
	if written > maxSelfBinarySize {
		os.Remove(destination)
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihari asset is too large"}
	}
	return nil
}
