package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// ReleaseTarget binds one update to an official release and asset observation.
// Its fields cannot be supplied by control clients or mutated after resolution.
type ReleaseTarget struct {
	repository, channel, tag, goos, goarch string
	releaseID                              int64
	asset                                  Asset
}

// ResolveTarget resolves the channel once for an online installation.
func (i Installer) ResolveTarget(ctx context.Context, channel string) (ReleaseTarget, error) {
	if i.repository() != "MetaCubeX/mihomo" {
		return ReleaseTarget{}, dataFailure("online core updates require the official mihomo repository")
	}
	if channel == "" {
		channel = "stable"
	}
	if channel != "stable" && channel != "alpha" {
		return ReleaseTarget{}, dataFailure("unsupported mihomo channel")
	}
	ctx, cancel := context.WithTimeout(ctx, i.checkTimeout())
	defer cancel()
	i.HTTPClient = i.targetHTTPClient()
	release, err := i.LatestRelease(ctx, channel)
	if err != nil {
		return ReleaseTarget{}, err
	}
	if release.ID <= 0 {
		return ReleaseTarget{}, dataFailure("mihomo release has no valid identity")
	}
	if channel == "alpha" {
		if release.TagName != "Prerelease-Alpha" {
			return ReleaseTarget{}, dataFailure("unexpected mihomo alpha release tag")
		}
	} else if version, err := ParseVersion(release.TagName); err != nil || version != release.TagName || !strings.HasPrefix(version, "v") {
		return ReleaseTarget{}, dataFailure("invalid mihomo stable release tag")
	}
	asset, err := SelectAsset(release, i.targetOS(), i.targetArch(), channel)
	if err != nil {
		return ReleaseTarget{}, err
	}
	if err := validateTargetAsset(asset); err != nil {
		return ReleaseTarget{}, err
	}
	return ReleaseTarget{repository: i.repository(), channel: channel, tag: release.TagName, goos: i.targetOS(), goarch: i.targetArch(), releaseID: release.ID, asset: asset}, nil
}

func validateTargetAsset(asset Asset) error {
	if asset.ID <= 0 || asset.State != "uploaded" {
		return dataFailure("mihomo release asset is not uploaded or has no valid identity")
	}
	if asset.Size <= 0 || asset.Size > maxCoreArchiveSize {
		return dataFailure("mihomo release asset has an invalid size")
	}
	if _, err := time.Parse(time.RFC3339Nano, asset.UpdatedAt); err != nil {
		return dataFailureCause("mihomo release asset has an invalid update time", err)
	}
	if asset.Digest != "" {
		value, ok := strings.CutPrefix(asset.Digest, "sha256:")
		if !ok || len(value) != sha256.Size*2 {
			return dataFailure("invalid mihomo asset digest")
		}
		if _, err := hex.DecodeString(value); err != nil {
			return dataFailureCause("invalid mihomo asset digest", err)
		}
	}
	return nil
}

func (i Installer) downloadTarget(ctx context.Context, target ReleaseTarget, destination string) error {
	if target.repository != "MetaCubeX/mihomo" || target.releaseID <= 0 {
		return dataFailure("invalid mihomo release target")
	}
	if err := validateTargetAsset(target.asset); err != nil {
		return err
	}
	i.HTTPClient = i.targetHTTPClient()
	if err := i.recheckTarget(ctx, target); err != nil {
		return err
	}
	asset := target.asset
	asset.URL = i.targetAssetURL(target)
	if err := i.downloadAsset(ctx, asset, destination, "application/octet-stream"); err != nil {
		return err
	}
	return i.recheckTarget(ctx, target)
}

// targetHTTPClient keeps caller transports and stricter redirect policies while
// enforcing the official download boundary. It never mutates a shared client.
func (i Installer) targetHTTPClient() *http.Client {
	client := *i.httpClient()
	previous := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		u := request.URL
		if len(via) >= 10 || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
			return dataFailure("unsafe mihomo asset redirect")
		}
		switch u.Hostname() {
		case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		default:
			return dataFailure("unofficial mihomo asset redirect")
		}
		if len(via) > 0 && u.Host != via[0].URL.Host {
			request.Header.Del("Authorization")
			request.Header.Del("Proxy-Authorization")
			request.Header.Del("Cookie")
		}
		if previous != nil {
			return previous(request, via)
		}
		return nil
	}
	return &client
}

func (i Installer) targetAssetURL(target ReleaseTarget) string {
	return fmt.Sprintf("%s/repos/%s/releases/assets/%d", strings.TrimRight(i.apiBase(), "/"), target.repository, target.asset.ID)
}

func (i Installer) recheckTarget(ctx context.Context, target ReleaseTarget) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, i.checkTimeout())
	defer cancel()
	assetURL := i.targetAssetURL(target)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return dataFailureCause("create mihomo asset metadata request", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	request.Header.Set("User-Agent", "mihari")
	response, err := i.httpClient().Do(request)
	if err != nil {
		return coreHTTPError(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "recheck mihomo asset failed"}, "core GET asset metadata", assetURL, "transport", nil, err)
	}
	defer closeCoreResponse(ctx, response, "core GET asset metadata", assetURL, &resultErr, i.Reporter)
	if response.StatusCode != http.StatusOK {
		return coreHTTPError(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "recheck mihomo asset failed"}, "core GET asset metadata", assetURL, "response", response, nil)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxReleaseResponseSize+1))
	if err != nil {
		return dataFailureCause("read mihomo asset metadata", err)
	}
	if len(raw) > maxReleaseResponseSize {
		return dataFailure("mihomo asset metadata is too large")
	}
	var observed Asset
	if err := json.Unmarshal(raw, &observed); err != nil {
		return dataFailureCause("invalid mihomo asset metadata", err)
	}
	expected := target.asset
	if observed.ID != expected.ID || observed.Name != expected.Name || observed.Size != expected.Size || observed.State != expected.State || observed.UpdatedAt != expected.UpdatedAt || observed.Digest != expected.Digest {
		return dataFailure("selected mihomo asset changed during update")
	}
	return nil
}
