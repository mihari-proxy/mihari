// Package update downloads and replaces the Mihari binary (self-update).
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	maxReleaseResponseSize  = 2 << 20
	maxSelfBinarySize       = 128 << 20
	maxChecksumManifestSize = 1 << 20
	defaultRepo             = "mihari-proxy/mihari"
	checksumAssetName       = "SHA256SUMS.txt"
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
	Draft   bool    `json:"draft"`
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
	// openCandidate is an optional test seam for candidate create/write/close failures.
	openCandidate func(string) (io.WriteCloser, error)
}

// Result describes a self-update attempt.
type Result struct {
	Version string
	Updated bool
	Ahead   bool
	Channel string
}

// CheckResult describes the latest Mihari release relative to the running version.
type CheckResult struct {
	Current   string
	Latest    string
	Available bool
	Ahead     bool
	Channel   string
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

// Check reports whether GitHub Releases contains a newer Mihari version for channel.
func (u SelfUpdater) Check(ctx context.Context, currentVersion, channel string) (CheckResult, error) {
	ch, err := normalizeChannel(channel)
	if err != nil {
		return CheckResult{}, err
	}
	release, err := u.latestRelease(ctx, ch)
	if err != nil {
		return CheckResult{}, err
	}
	available, ahead := classifyUpdate(currentVersion, release.TagName)
	return CheckResult{
		Current:   currentVersion,
		Latest:    release.TagName,
		Available: available,
		Ahead:     ahead,
		Channel:   ch,
	}, nil
}

// Update downloads the latest release when newer than currentVersion and replaces binaryPath.
func (u SelfUpdater) Update(ctx context.Context, binaryPath, currentVersion, channel string) (Result, error) {
	ch, err := normalizeChannel(channel)
	if err != nil {
		return Result{}, err
	}
	release, err := u.latestRelease(ctx, ch)
	if err != nil {
		return Result{}, err
	}
	available, ahead := classifyUpdate(currentVersion, release.TagName)
	if !available {
		return Result{Version: release.TagName, Updated: false, Ahead: ahead, Channel: ch}, nil
	}
	asset, err := SelectSelfAsset(release, u.targetOS(), u.targetArch())
	if err != nil {
		return Result{}, err
	}
	if asset.Size < 0 || asset.Size > maxSelfBinarySize {
		return Result{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihari asset is too large"}
	}
	checksumAsset, err := selectChecksumAsset(release)
	if err != nil {
		return Result{}, err
	}
	expected, err := u.fetchExpectedChecksum(ctx, checksumAsset, asset.Name)
	if err != nil {
		return Result{}, err
	}
	stagingDir := filepath.Join(filepath.Dir(binaryPath), ".mihari-update")
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create self-update staging: %w", err)
	}
	defer os.RemoveAll(stagingDir)

	candidate := filepath.Join(stagingDir, filepath.Base(binaryPath)+".new")
	if err := u.download(ctx, asset, expected, candidate); err != nil {
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
			return Result{Version: release.TagName, Updated: true, Channel: ch}, err
		}
	}
	return Result{Version: release.TagName, Updated: true, Channel: ch}, nil
}

func sameTag(current, latest string) bool {
	a := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(current)), "v")
	b := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(latest)), "v")
	return a != "" && a == b
}

func selfAssetName(goos, goarch string) string {
	goos = strings.ToLower(goos)
	goarch = strings.ToLower(goarch)
	name := "mihari-" + goos + "-" + goarch
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

// SelectSelfAsset picks the unique mihari binary asset for goos/goarch.
// Expected names are mihari-windows-<arch>.exe or mihari-<goos>-<arch>.
func SelectSelfAsset(release Release, goos, goarch string) (Asset, error) {
	want := selfAssetName(goos, goarch)
	var found Asset
	matches := 0
	for _, asset := range release.Assets {
		if asset.Name != want {
			continue
		}
		found = asset
		matches++
	}
	if matches != 1 {
		return Asset{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihari release has no compatible asset"}
	}
	return found, nil
}

func selectChecksumAsset(release Release) (Asset, error) {
	var found Asset
	matches := 0
	for _, asset := range release.Assets {
		if asset.Name != checksumAssetName {
			continue
		}
		found = asset
		matches++
	}
	if matches != 1 {
		return Asset{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihari release has no unique checksum manifest"}
	}
	if found.Size < 0 || found.Size > maxChecksumManifestSize {
		return Asset{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "checksum manifest is too large"}
	}
	return found, nil
}

func parseChecksumManifest(raw []byte, targetName string) ([sha256.Size]byte, error) {
	var found [sha256.Size]byte
	matches := 0
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return [sha256.Size]byte{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid checksum manifest"}
		}
		decoded, err := hex.DecodeString(fields[0])
		if err != nil || len(decoded) != sha256.Size {
			return [sha256.Size]byte{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid checksum manifest"}
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != targetName {
			continue
		}
		copy(found[:], decoded)
		matches++
	}
	if matches != 1 {
		return [sha256.Size]byte{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "checksum manifest has no unique target digest"}
	}
	return found, nil
}

func (u SelfUpdater) fetchExpectedChecksum(ctx context.Context, asset Asset, targetName string) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return zero, protocol.APIError{Code: protocol.CodeInternal, Message: "create checksum request"}
	}
	request.Header.Set("User-Agent", "mihari")
	response, err := u.httpClient().Do(request)
	if err != nil {
		return zero, protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download checksum manifest failed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return zero, protocol.APIError{
			Code:    protocol.CodeNetworkFailure,
			Message: "download checksum manifest failed",
			Details: map[string]any{"status": response.StatusCode},
		}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxChecksumManifestSize+1))
	if err != nil {
		return zero, protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "read checksum manifest failed"}
	}
	if len(raw) > maxChecksumManifestSize {
		return zero, protocol.APIError{Code: protocol.CodeDataFailure, Message: "checksum manifest is too large"}
	}
	return parseChecksumManifest(raw, targetName)
}

func (u SelfUpdater) latestRelease(ctx context.Context, channel string) (Release, error) {
	switch channel {
	case ChannelMain, ChannelDev:
		return u.latestMainRelease(ctx)
	default:
		return Release{}, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "mihari channel must be main or dev"}
	}
}

func (u SelfUpdater) latestMainRelease(ctx context.Context) (Release, error) {
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

func (u SelfUpdater) openCandidateFile(destination string) (io.WriteCloser, error) {
	if u.openCandidate != nil {
		return u.openCandidate(destination)
	}
	return os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
}

func (u SelfUpdater) download(ctx context.Context, asset Asset, expected [sha256.Size]byte, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
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
			Code:    protocol.CodeNetworkFailure,
			Message: "download mihari asset failed",
			Details: map[string]any{"status": response.StatusCode},
		}
	}
	file, err := u.openCandidateFile(destination)
	if err != nil {
		return fmt.Errorf("create mihari candidate: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxSelfBinarySize+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(destination)
		return protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "read mihari asset failed"}
	}
	if written > maxSelfBinarySize {
		os.Remove(destination)
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihari asset is too large"}
	}
	if asset.Size > 0 && written != asset.Size {
		os.Remove(destination)
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihari asset size mismatch"}
	}
	var got [sha256.Size]byte
	copy(got[:], hash.Sum(nil))
	if got != expected {
		os.Remove(destination)
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihari asset digest mismatch"}
	}
	return nil
}
