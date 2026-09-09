package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// RebuildMigrationCore verifies a compressed artifact against the compiled Unix
// table and derives both core bytes and provenance. It never executes the core.
func RebuildMigrationCore(ctx context.Context, goos, arch, tag string, archive []byte) ([]byte, []byte, error) {
	a, err := supportedCore(ctx, goos, arch, tag, "stable")
	if err != nil {
		return nil, nil, err
	}
	return rebuildMigrationCore(ctx, a, archive)
}

// DownloadMigrationCore retrieves only the pinned supported asset, then derives
// a receipt from the independently compiled digest rather than a source receipt.
func DownloadMigrationCore(ctx context.Context, client *http.Client, goos, arch, tag string) ([]byte, []byte, error) {
	a, err := supportedCore(ctx, goos, arch, tag, "stable")
	if err != nil {
		return nil, nil, err
	}
	c := http.Client{Timeout: 2 * time.Minute}
	if client != nil {
		c = *client
		if c.Timeout == 0 {
			c.Timeout = 2 * time.Minute
		}
	}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		host := req.URL.Hostname()
		if len(via) > 5 || req.URL.Scheme != "https" || req.URL.User != nil || req.URL.Port() != "" || (host != "github.com" && host != "release-assets.githubusercontent.com" && host != "objects.githubusercontent.com") {
			return dataFailure("untrusted core redirect")
		}
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return nil, nil, err
	}
	response, err := c.Do(request)
	if err != nil {
		return nil, nil, dataFailure("download supported migration core")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, nil, dataFailure("download supported migration core")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxCoreArchiveSize+1))
	if err != nil {
		return nil, nil, err
	}
	return rebuildMigrationCore(ctx, a, raw)
}
func rebuildMigrationCore(ctx context.Context, a supportedAsset, archive []byte) ([]byte, []byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if len(archive) > maxCoreArchiveSize || int64(len(archive)) != a.Size || digest(archive) != a.AssetSHA256 {
		return nil, nil, dataFailure("mihomo compiled asset hash mismatch")
	}
	reader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, nil, dataFailure("invalid trusted core archive")
	}
	defer reader.Close()
	binary, err := io.ReadAll(io.LimitReader(reader, maxCoreBinarySize+1))
	if err != nil || len(binary) == 0 || len(binary) > maxCoreBinarySize {
		return nil, nil, dataFailure("invalid trusted core binary")
	}
	receipt, err := json.Marshal(ProvenanceReceipt{Schema: provenanceSchema, PolicyID: legacyReceiptPolicyID, AssetSHA256: a.AssetSHA256, BinarySHA256: digest(binary), OS: a.OS, Arch: a.Arch, Tag: a.Tag})
	if err != nil {
		return nil, nil, err
	}
	return binary, receipt, nil
}
