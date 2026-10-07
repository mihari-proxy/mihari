package core

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

const (
	maxCoreArchiveSize = 128 << 20
	maxCoreBinarySize  = 256 << 20
)

type Installer struct {
	// Provenance binds protected staging and execution to a verified data root.
	Provenance ProvenanceStore
	// Updates enables daemon-owned transactions without changing execution policy.
	Updates         ProvenanceStore
	GeneratedConfig func(context.Context) (*ConfigCapability, error)
	Executor        VerifiedExecutor
	Reporter        diagnostics.Reporter

	HTTPClient   *http.Client
	APIBase      string
	Repository   string
	GOOS         string
	GOARCH       string
	Runner       CommandRunner
	CheckTimeout time.Duration // 包住"检查最新版"请求，默认 8s；下载仍用 httpClient 超时（design §4.3）
}

type InstallRequest struct {
	BinaryPath     string
	DataDir        string
	ConfigPath     string
	StagingDir     string
	CurrentVersion string
	Channel        string
	AlphaSHA       string
}

type InstallResult struct {
	Version  string
	Updated  bool
	AlphaSHA string
}

// LocalCoreInfo reports whether an existing local core binary satisfies setup
// without a network download. Ready mirrors the Install setup fast-path predicate
// (DetectVersion success + non-empty version), kept DRY with manager.Install
// (design §4.3). Read-only; never persists or mutates state.
type LocalCoreInfo struct {
	Ready   bool
	Version string
}

func (i Installer) Install(ctx context.Context, request InstallRequest) (InstallResult, error) {
	candidate, err := i.Prepare(ctx, request)
	if err != nil {
		return InstallResult{}, err
	}
	defer candidate.Cleanup()
	return candidate.Commit()
}

// DetectVersion reports a local core version after the configured platform checks.
// Setup uses it to recognize an existing offline or administrator-deployed core.
func (i Installer) DetectVersion(ctx context.Context, binaryPath string) (string, error) {
	if err := i.CheckExecution(ctx); err != nil {
		return "", err
	}
	if i.Provenance != nil {
		v, e := OpenInstalledCore(ctx, i.Provenance)
		if e != nil {
			return "", e
		}
		defer func() { _ = v.Close() }() // Read-only capability: no pending writes; closure cannot change the operation result.
		return DetectVerifiedVersion(ctx, v, i.Executor)
	}

	runner := i.Runner
	if runner == nil {
		runner = OSCommandRunner{}
	}
	return DetectVersion(ctx, runner, binaryPath)
}

// CheckExecution refuses an incomplete update outside its owning lifecycle.
// Normal ordinary-platform starts retain their existing execution policy.
func (i Installer) CheckExecution(ctx context.Context) error {
	if i.Updates != nil {
		return authorizePendingUpdate(ctx, i.Updates)
	}
	return nil
}

type Candidate struct {
	trusted   *trustedCandidate
	protected *protectedCandidate

	path       string
	binaryPath string
	version    string
	alphaSHA   string
	updated    bool
	cleanup    sync.Once
	reporter   diagnostics.Reporter
	warnings   []error
}

type PreparedCore interface {
	Version() string
	Updated() bool
	Commit() (InstallResult, error)
	Cleanup()
}

func (c *Candidate) Version() string { return c.version }

func (c *Candidate) Updated() bool { return c.updated }

// Warnings returns preparation diagnostics for the operation owner to publish.
func (c *Candidate) Warnings() []error { return append([]error(nil), c.warnings...) }

func (c *Candidate) Commit() (InstallResult, error) {
	if c.protected != nil {
		return InstallResult{}, dataFailure("core update requires the runtime transaction owner")
	}
	if c.trusted != nil {
		return c.commitTrusted()
	}

	if !c.updated {
		return InstallResult{Version: c.version, Updated: false, AlphaSHA: c.alphaSHA}, nil
	}
	if c.path == "" {
		return InstallResult{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo candidate is unavailable"}
	}
	if err := os.MkdirAll(filepath.Dir(c.binaryPath), 0o700); err != nil {
		return InstallResult{}, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "create core binary directory"}, err)
	}
	warning, err := replaceBinary(c.path, c.binaryPath)
	if err != nil {
		return InstallResult{}, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "replace mihomo core"}, err)
	}
	c.path = ""
	if warning != nil && c.reporter != nil {
		c.reporter(context.Background(), diagnostics.Record{Component: "core", Event: "replacement.cleanup.failed", Level: slog.LevelWarn, Err: fmt.Errorf("remove replaced core backup: %w", warning)})
	}
	return InstallResult{Version: c.version, Updated: true, AlphaSHA: c.alphaSHA}, nil
}

func (c *Candidate) Cleanup() {
	if c.protected != nil {
		c.cleanup.Do(c.cleanupProtected)
		return
	}
	if c.trusted != nil {
		c.cleanupTrusted()
		return
	}

	c.cleanup.Do(func() {
		if c.path != "" {
			if err := os.Remove(c.path); err != nil && !errors.Is(err, os.ErrNotExist) && c.reporter != nil {
				c.reporter(context.Background(), diagnostics.Record{Component: "core", Event: "candidate.cleanup.failed", Level: slog.LevelWarn, Err: fmt.Errorf("remove core candidate %s: %w", c.path, err)})
			}
		}
	})
}

func (i Installer) Prepare(ctx context.Context, request InstallRequest) (PreparedCore, error) {
	target, err := i.ResolveTarget(ctx, request.Channel)
	if err != nil {
		return nil, withAIOHint(err)
	}
	if i.Provenance != nil {
		return i.prepareProtected(ctx, target)
	}
	if i.Updates != nil {
		return i.prepareFileUpdate(ctx, target, request)
	}
	asset := target.asset
	if err := os.MkdirAll(request.StagingDir, 0o700); err != nil {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "create core staging directory"}, err)
	}
	archive, err := os.CreateTemp(request.StagingDir, ".mihomo-download-*")
	if err != nil {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "create core download file"}, err)
	}
	archivePath := archive.Name()
	if err := archive.Close(); err != nil {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "close core download file"}, errors.Join(err, os.Remove(archivePath)))
	}
	defer func() {
		if err := os.Remove(archivePath); err != nil && !errors.Is(err, os.ErrNotExist) && i.Reporter != nil {
			i.Reporter(context.Background(), diagnostics.Record{Component: "core", Event: "archive.cleanup.failed", Level: slog.LevelWarn, Err: fmt.Errorf("remove core archive %s: %w", archivePath, err)})
		}
	}()
	if err := i.downloadTarget(ctx, target, archivePath); err != nil {
		return nil, err
	}

	candidate, err := os.CreateTemp(request.StagingDir, ".mihomo-candidate-*")
	if err != nil {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "create core candidate"}, err)
	}
	candidatePath := candidate.Name()
	if err := candidate.Close(); err != nil {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "close core candidate"}, errors.Join(err, os.Remove(candidatePath)))
	}
	keepCandidate := false
	defer func() {
		if !keepCandidate {
			if err := os.Remove(candidatePath); err != nil && !errors.Is(err, os.ErrNotExist) && i.Reporter != nil {
				i.Reporter(context.Background(), diagnostics.Record{Component: "core", Event: "candidate.cleanup.failed", Level: slog.LevelWarn, Err: fmt.Errorf("remove core candidate %s: %w", candidatePath, err)})
			}
		}
	}()
	if err := extractAsset(archivePath, asset.Name, candidatePath); err != nil {
		return nil, err
	}
	if err := os.Chmod(candidatePath, 0o700); err != nil {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "set core executable permissions"}, err)
	}
	runner := i.Runner
	if runner == nil {
		runner = OSCommandRunner{}
	}
	versionOutput, err := runner.Run(ctx, candidatePath, "-v")
	if err != nil {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo candidate did not start"}, errors.Join(err, commandOutputCause("mihomo candidate version", versionOutput)))
	}
	if len(strings.TrimSpace(string(versionOutput))) == 0 {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo candidate did not start"}
	}
	version, err := ParseVersion(string(versionOutput))
	if err != nil {
		return nil, err
	}
	expectedVersion := target.tag
	if request.Channel == "alpha" {
		expectedVersion = "alpha-" + ParseAlphaSHA(asset.Name)
	}
	if version != expectedVersion {
		return nil, dataFailureCause("mihomo candidate version does not match selected release", fmt.Errorf("selected %s, candidate reported %s", expectedVersion, version))
	}
	if err := ValidateConfig(ctx, runner, candidatePath, request.DataDir, request.ConfigPath); err != nil {
		return nil, err
	}
	keepCandidate = true
	prepared := &Candidate{path: candidatePath, binaryPath: request.BinaryPath, version: version, alphaSHA: ParseAlphaSHA(asset.Name), updated: true, reporter: i.Reporter}
	if asset.Digest == "" {
		prepared.warnings = []error{missingDigestWarning(asset)}
	}
	return prepared, nil
}

// Download 取 asset 并落盘到 destination，校验 asset.Digest 的 sha256:<hex>
// （bundler 复用入口，design §4.1 export 边界；绝不照 self.go 复刻——其无 Digest 校验）。
// 以 O_WRONLY|O_TRUNC 写入：调用方需先落盘目标文件（与 Prepare 内 CreateTemp 同契约）。
func (i Installer) Download(ctx context.Context, asset Asset, destination string) (resultErr error) {
	return i.downloadAsset(ctx, asset, destination, "")
}

func (i Installer) downloadAsset(ctx context.Context, asset Asset, destination, accept string) (resultErr error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInternal, Message: "create core download request"}, err)
	}
	request.Header.Set("User-Agent", "mihari")
	if accept != "" {
		request.Header.Set("Accept", accept)
		request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	}
	response, err := i.httpClient().Do(request)
	if err != nil {
		return coreHTTPError(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download mihomo core failed"}, "core GET asset", asset.URL, "transport", nil, err)
	}
	defer closeCoreResponse(ctx, response, "core GET asset", asset.URL, &resultErr, i.Reporter)
	if response.StatusCode != http.StatusOK {
		return coreHTTPError(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download mihomo core failed", Details: map[string]any{"status": response.StatusCode}}, "core GET asset", asset.URL, "response", response, nil)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "open core download file"}, err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxCoreArchiveSize+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "save mihomo core download failed"}, errors.Join(copyErr, closeErr))
	}
	if written > maxCoreArchiveSize || (asset.Size > 0 && written != asset.Size) {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo asset size mismatch"}
	}
	if asset.Digest != "" {
		algorithm, expected, found := strings.Cut(asset.Digest, ":")
		if !found || algorithm != "sha256" {
			return protocol.APIError{Code: protocol.CodeDataFailure, Message: "unsupported mihomo asset digest"}
		}
		actual := hex.EncodeToString(hash.Sum(nil))
		if !strings.EqualFold(actual, expected) {
			return protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo asset digest mismatch"}
		}
	}
	return nil
}

func extractAsset(archivePath, assetName, candidatePath string) error {
	if strings.HasSuffix(strings.ToLower(assetName), ".gz") {
		return extractGzip(archivePath, candidatePath)
	}
	if strings.HasSuffix(strings.ToLower(assetName), ".zip") {
		return extractZip(archivePath, candidatePath)
	}
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: "unsupported mihomo archive"}
}

func extractGzip(archivePath, candidatePath string) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return dataFailureCause("open mihomo archive", err)
	}
	defer archive.Close() // A read-only source close cannot invalidate a completed candidate write.
	reader, err := gzip.NewReader(archive)
	if err != nil {
		return dataFailureCause("invalid mihomo gzip archive", err)
	}
	defer reader.Close()
	return writeCandidate(candidatePath, reader)
}

func extractZip(archivePath, candidatePath string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return dataFailureCause("invalid mihomo zip archive", err)
	}
	defer archive.Close()
	var selected *zip.File
	for _, file := range archive.File {
		if !safeArchiveName(file.Name) {
			return dataFailure("unsafe path in mihomo archive")
		}
		base := strings.ToLower(filepath.Base(file.Name))
		if !file.FileInfo().IsDir() && strings.Contains(base, "mihomo") && strings.HasSuffix(base, ".exe") && selected == nil {
			selected = file
		}
	}
	if selected == nil {
		return dataFailure("mihomo executable is missing from archive")
	}
	reader, err := selected.Open()
	if err != nil {
		return dataFailureCause("open mihomo executable in archive", err)
	}
	defer reader.Close()
	return writeCandidate(candidatePath, reader)
}

func safeArchiveName(name string) bool {
	forward := strings.ReplaceAll(name, "\\", "/")
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(forward)))
	return clean != ".." && !strings.HasPrefix(clean, "../") && !strings.HasPrefix(forward, "/") && !filepath.IsAbs(filepath.FromSlash(forward))
}

func writeCandidate(path string, source io.Reader) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return dataFailureCause("open mihomo candidate", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, maxCoreBinarySize+1))
	if syncErr := file.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := file.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return dataFailureCause("write mihomo candidate", copyErr)
	}
	if written > maxCoreBinarySize {
		return dataFailure("mihomo executable is too large")
	}
	return nil
}

func (i Installer) httpClient() *http.Client {
	if i.HTTPClient != nil {
		return i.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Minute}
}

// checkTimeout 返回"检查最新版"请求的超时（默认 8s）；下载仍用 httpClient 的 15min 超时（design §4.3）。
func (i Installer) checkTimeout() time.Duration {
	if i.CheckTimeout > 0 {
		return i.CheckTimeout
	}
	return 8 * time.Second
}

// withAIOHint 给网络类失败的错误信息追加 aio 离线安装脚本引导（design §6 错误处理）。
func withAIOHint(err error) error {
	var apiError protocol.APIError
	if errors.As(err, &apiError) && apiError.Code == protocol.CodeNetworkFailure {
		apiError.Message += "; for offline or restricted networks, use the all-in-one installer (install-aio-remote.sh / .ps1)"
		return diagnostics.Wrap(apiError, err)
	}
	return err
}

func (i Installer) apiBase() string {
	if i.APIBase != "" {
		return i.APIBase
	}
	return "https://api.github.com"
}

func (i Installer) repository() string {
	if i.Repository != "" {
		return i.Repository
	}
	return "MetaCubeX/mihomo"
}

func (i Installer) targetOS() string {
	if i.GOOS != "" {
		return i.GOOS
	}
	return runtime.GOOS
}

func (i Installer) targetArch() string {
	if i.GOARCH != "" {
		return i.GOARCH
	}
	return runtime.GOARCH
}

func dataFailure(message string) error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: message}
}

func dataFailureCause(message string, cause error) error {
	return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: message}, cause)
}

// UpdateStore returns the daemon-owned store used for explicit reinstall.
func (i Installer) UpdateStore() ProvenanceStore {
	if i.Provenance != nil {
		return i.Provenance
	}
	return i.Updates
}
