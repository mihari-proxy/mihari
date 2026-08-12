package core

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const (
	maxCoreArchiveSize = 128 << 20
	maxCoreBinarySize  = 256 << 20
)

type Installer struct {
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
}

type InstallResult struct {
	Version string
	Updated bool
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

// DetectVersion 报告现有核心二进制的版本（运行 mihomo -v）。供 runtime 侧 setup 本地预检
// 复用，避免在 runtime.Manager 里另持 Runner；判据复用 DetectVersion（含 ParseVersion 版本
// 格式校验），与 Prepare 的同版本短路同一判据、DRY（design §4.3 实现位置）。
func (i Installer) DetectVersion(ctx context.Context, binaryPath string) (string, error) {
	runner := i.Runner
	if runner == nil {
		runner = OSCommandRunner{}
	}
	return DetectVersion(ctx, runner, binaryPath)
}

type Candidate struct {
	path       string
	binaryPath string
	version    string
	updated    bool
	cleanup    sync.Once
}

type PreparedCore interface {
	Version() string
	Updated() bool
	Commit() (InstallResult, error)
	Cleanup()
}

func (c *Candidate) Version() string { return c.version }

func (c *Candidate) Updated() bool { return c.updated }

func (c *Candidate) Commit() (InstallResult, error) {
	if !c.updated {
		return InstallResult{Version: c.version, Updated: false}, nil
	}
	if c.path == "" {
		return InstallResult{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo candidate is unavailable"}
	}
	if err := os.MkdirAll(filepath.Dir(c.binaryPath), 0o700); err != nil {
		return InstallResult{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "create core binary directory"}
	}
	if err := replaceBinary(c.path, c.binaryPath); err != nil {
		return InstallResult{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "replace mihomo core"}
	}
	c.path = ""
	return InstallResult{Version: c.version, Updated: true}, nil
}

func (c *Candidate) Cleanup() {
	c.cleanup.Do(func() {
		if c.path != "" {
			_ = os.Remove(c.path)
		}
	})
}

func (i Installer) Prepare(ctx context.Context, request InstallRequest) (PreparedCore, error) {
	checkCtx, cancel := context.WithTimeout(ctx, i.checkTimeout())
	release, err := i.LatestRelease(checkCtx)
	cancel()
	if err != nil {
		return nil, withAIOHint(err)
	}
	if request.CurrentVersion == release.TagName {
		if info, statErr := os.Stat(request.BinaryPath); statErr == nil && !info.IsDir() {
			// 文件存在还要 -v 成功才短路；判据复用 DetectVersion（含 ParseVersion 版本格式校验）
			// ——design §4.3，与 Manager.Install setup 预检同一判据、DRY。-v 失败 → 走下载修复。
			runner := i.Runner
			if runner == nil {
				runner = OSCommandRunner{}
			}
			if version, vErr := DetectVersion(ctx, runner, request.BinaryPath); vErr == nil && version != "" {
				return &Candidate{binaryPath: request.BinaryPath, version: release.TagName, updated: false}, nil
			}
		}
	}
	asset, err := SelectAsset(release, i.targetOS(), i.targetArch())
	if err != nil {
		return nil, err
	}
	if asset.Size < 0 || asset.Size > maxCoreArchiveSize {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo asset is too large"}
	}
	if err := os.MkdirAll(request.StagingDir, 0o700); err != nil {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "create core staging directory"}
	}
	archive, err := os.CreateTemp(request.StagingDir, ".mihomo-download-*")
	if err != nil {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "create core download file"}
	}
	archivePath := archive.Name()
	archive.Close()
	defer os.Remove(archivePath)
	if err := i.Download(ctx, asset, archivePath); err != nil {
		return nil, err
	}

	candidate, err := os.CreateTemp(request.StagingDir, ".mihomo-candidate-*")
	if err != nil {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "create core candidate"}
	}
	candidatePath := candidate.Name()
	candidate.Close()
	keepCandidate := false
	defer func() {
		if !keepCandidate {
			_ = os.Remove(candidatePath)
		}
	}()
	if err := extractAsset(archivePath, asset.Name, candidatePath); err != nil {
		return nil, err
	}
	if err := os.Chmod(candidatePath, 0o700); err != nil {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "set core executable permissions"}
	}
	runner := i.Runner
	if runner == nil {
		runner = OSCommandRunner{}
	}
	versionOutput, err := runner.Run(ctx, candidatePath, "-v")
	if err != nil || len(strings.TrimSpace(string(versionOutput))) == 0 {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo candidate did not start"}
	}
	if err := ValidateConfig(ctx, runner, candidatePath, request.DataDir, request.ConfigPath); err != nil {
		return nil, err
	}
	keepCandidate = true
	return &Candidate{path: candidatePath, binaryPath: request.BinaryPath, version: release.TagName, updated: true}, nil
}

// Download 取 asset 并落盘到 destination，校验 asset.Digest 的 sha256:<hex>
// （bundler 复用入口，design §4.1 export 边界；绝不照 self.go 复刻——其无 Digest 校验）。
// 以 O_WRONLY|O_TRUNC 写入：调用方需先落盘目标文件（与 Prepare 内 CreateTemp 同契约）。
func (i Installer) Download(ctx context.Context, asset Asset, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return protocol.APIError{Code: protocol.CodeInternal, Message: "create core download request"}
	}
	request.Header.Set("User-Agent", "mihari")
	response, err := i.httpClient().Do(request)
	if err != nil {
		return protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download mihomo core failed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download mihomo core failed", Details: map[string]any{"status": response.StatusCode}}
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "open core download file"}
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxCoreArchiveSize+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "save mihomo core download failed"}
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
		return dataFailure("open mihomo archive")
	}
	defer archive.Close()
	reader, err := gzip.NewReader(archive)
	if err != nil {
		return dataFailure("invalid mihomo gzip archive")
	}
	defer reader.Close()
	return writeCandidate(candidatePath, reader)
}

func extractZip(archivePath, candidatePath string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return dataFailure("invalid mihomo zip archive")
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
		return dataFailure("open mihomo executable in archive")
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
		return dataFailure("open mihomo candidate")
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, maxCoreBinarySize+1))
	if syncErr := file.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := file.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return dataFailure("write mihomo candidate")
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
		apiError.Message += "；若处于无网/受限网络环境，请使用 all-in-one 安装脚本（install-aio-remote.sh / .ps1）离线安装"
		return apiError
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
