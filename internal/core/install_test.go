package core

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestSelectAssetForReleaseTargets(t *testing.T) {
	release := Release{TagName: "v1.19.0", Assets: []Asset{
		{Name: "mihomo-linux-amd64-compatible-v1.19.0.gz"},
		{Name: "mihomo-linux-arm64-v1.19.0.gz"},
		{Name: "mihomo-darwin-amd64-compatible-v1.19.0.gz"},
		{Name: "mihomo-darwin-arm64-v1.19.0.gz"},
		{Name: "mihomo-windows-amd64-compatible-v1.19.0.zip"},
		{Name: "mihomo-windows-arm64-v1.19.0.zip"},
	}}
	for _, target := range []struct{ goos, goarch, want string }{
		{"linux", "amd64", "mihomo-linux-amd64-compatible-v1.19.0.gz"},
		{"linux", "arm64", "mihomo-linux-arm64-v1.19.0.gz"},
		{"darwin", "amd64", "mihomo-darwin-amd64-compatible-v1.19.0.gz"},
		{"darwin", "arm64", "mihomo-darwin-arm64-v1.19.0.gz"},
		{"windows", "amd64", "mihomo-windows-amd64-compatible-v1.19.0.zip"},
		{"windows", "arm64", "mihomo-windows-arm64-v1.19.0.zip"},
	} {
		t.Run(target.goos+"/"+target.goarch, func(t *testing.T) {
			asset, err := SelectAsset(release, target.goos, target.goarch)
			if err != nil || asset.Name != target.want {
				t.Fatalf("asset=%#v err=%v", asset, err)
			}
		})
	}
}

func TestInstallerDownloadsExtractsValidatesAndReplaces(t *testing.T) {
	for _, target := range []struct {
		goos    string
		goarch  string
		name    string
		archive func(t *testing.T, content []byte) []byte
	}{
		{"linux", "amd64", "mihomo-linux-amd64-compatible-v1.19.0.gz", gzipFixture},
		{"darwin", "arm64", "mihomo-darwin-arm64-v1.19.0.gz", gzipFixture},
		{"windows", "amd64", "mihomo-windows-amd64-compatible-v1.19.0.zip", zipFixture},
	} {
		t.Run(target.goos+"/"+target.goarch, func(t *testing.T) {
			binary := []byte("fake-mihomo-binary")
			archive := target.archive(t, binary)
			server := releaseFixture(t, target.name, archive)
			defer server.Close()

			root := t.TempDir()
			binaryPath := filepath.Join(root, "bin", "mihomo")
			if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(binaryPath, []byte("old-binary"), 0o700); err != nil {
				t.Fatal(err)
			}
			runner := &recordingRunner{output: []byte("Mihomo Meta v1.19.0")}
			installer := Installer{
				HTTPClient: server.Client(), APIBase: server.URL, Repository: "MetaCubeX/mihomo",
				GOOS: target.goos, GOARCH: target.goarch, Runner: runner,
			}
			result, err := installer.Install(context.Background(), InstallRequest{
				BinaryPath: binaryPath,
				DataDir:    root,
				ConfigPath: filepath.Join(root, "config.yaml"),
				StagingDir: filepath.Join(root, "staging"),
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Version != "v1.19.0" || !result.Updated {
				t.Fatalf("result=%#v", result)
			}
			got, err := os.ReadFile(binaryPath)
			if err != nil || !bytes.Equal(got, binary) {
				t.Fatalf("binary=%q err=%v", got, err)
			}
			if len(runner.args) == 0 {
				t.Fatal("candidate was not validated")
			}
		})
	}
}

func TestInstallerPreservesOldBinaryOnCorruptArchive(t *testing.T) {
	server := releaseFixture(t, "mihomo-linux-amd64-compatible-v1.19.0.gz", []byte("not-gzip"))
	defer server.Close()
	root := t.TempDir()
	binaryPath := filepath.Join(root, "mihomo")
	if err := os.WriteFile(binaryPath, []byte("working"), 0o700); err != nil {
		t.Fatal(err)
	}
	installer := Installer{HTTPClient: server.Client(), APIBase: server.URL, Repository: "MetaCubeX/mihomo", GOOS: "linux", GOARCH: "amd64", Runner: &recordingRunner{}}
	_, err := installer.Install(context.Background(), InstallRequest{BinaryPath: binaryPath, DataDir: root, ConfigPath: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging")})
	if err == nil {
		t.Fatal("expected corrupt archive error")
	}
	got, readErr := os.ReadFile(binaryPath)
	if readErr != nil || string(got) != "working" {
		t.Fatalf("binary=%q err=%v", got, readErr)
	}
}

func TestInstallerPrepareDoesNotReplaceUntilCommit(t *testing.T) {
	archive := gzipFixture(t, []byte("new-binary"))
	server := releaseFixture(t, "mihomo-linux-amd64-compatible-v1.19.0.gz", archive)
	defer server.Close()
	root := t.TempDir()
	binaryPath := filepath.Join(root, "mihomo")
	if err := os.WriteFile(binaryPath, []byte("old-binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	installer := Installer{HTTPClient: server.Client(), APIBase: server.URL, Repository: "MetaCubeX/mihomo", GOOS: "linux", GOARCH: "amd64", Runner: &recordingRunner{output: []byte("Mihomo Meta v1.19.0")}}
	candidate, err := installer.Prepare(context.Background(), InstallRequest{BinaryPath: binaryPath, DataDir: root, ConfigPath: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging")})
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.Cleanup()
	before, err := os.ReadFile(binaryPath)
	if err != nil || string(before) != "old-binary" {
		t.Fatalf("before commit=%q err=%v", before, err)
	}
	result, err := candidate.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "v1.19.0" || !result.Updated {
		t.Fatalf("result=%#v", result)
	}
	after, err := os.ReadFile(binaryPath)
	if err != nil || string(after) != "new-binary" {
		t.Fatalf("after commit=%q err=%v", after, err)
	}
}

func TestInstallerRejectsUnsafeZipMember(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create("../outside.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("unsafe"))
	entry, err = writer.Create("mihomo-windows-amd64-compatible.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("binary"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	server := releaseFixture(t, "mihomo-windows-amd64-compatible-v1.19.0.zip", archive.Bytes())
	defer server.Close()
	root := t.TempDir()
	installer := Installer{HTTPClient: server.Client(), APIBase: server.URL, Repository: "MetaCubeX/mihomo", GOOS: "windows", GOARCH: "amd64", Runner: &recordingRunner{}}
	_, err = installer.Install(context.Background(), InstallRequest{BinaryPath: filepath.Join(root, "mihomo.exe"), DataDir: root, ConfigPath: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging")})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(root), "outside.exe")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unsafe path created: %v", statErr)
	}
}

func releaseFixture(t *testing.T, assetName string, archive []byte) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/MetaCubeX/mihomo/releases/latest":
			if request.Header.Get("Accept") != "application/vnd.github+json" || request.Header.Get("X-GitHub-Api-Version") == "" {
				t.Errorf("missing GitHub API headers")
			}
			_ = json.NewEncoder(response).Encode(Release{TagName: "v1.19.0", Assets: []Asset{{Name: assetName, URL: server.URL + "/asset", Size: int64(len(archive))}}})
		case "/asset":
			_, _ = response.Write(archive)
		default:
			http.NotFound(response, request)
		}
	}))
	return server
}

func gzipFixture(t *testing.T, content []byte) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := gzip.NewWriter(&archive)
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func zipFixture(t *testing.T, content []byte) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create("mihomo-windows-amd64-compatible.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func TestInstallerDetectVersionReportsExistingBinary(t *testing.T) {
	runner := &recordingRunner{output: []byte("Mihomo Meta v1.18.2")}
	version, err := Installer{Runner: runner}.DetectVersion(context.Background(), "mihomo")
	if err != nil {
		t.Fatal(err)
	}
	if version != "v1.18.2" {
		t.Fatalf("version=%q", version)
	}
	if len(runner.args) != 1 || runner.args[0] != "-v" {
		t.Fatalf("args=%q", runner.args)
	}

	failing := &recordingRunner{err: errors.New("exec failed")}
	_, err = Installer{Runner: failing}.DetectVersion(context.Background(), "mihomo")
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
		t.Fatalf("err=%v", err)
	}
}

func TestPrepare(t *testing.T) {
	t.Run("API timeout with local binary fails fast with aio hint", func(t *testing.T) {
		archive := gzipFixture(t, []byte("payload"))
		server := slowReleaseServer(t, "mihomo-linux-amd64-compatible-v1.19.0.gz", archive, 150*time.Millisecond)
		defer server.Close()
		root := t.TempDir()
		binaryPath := filepath.Join(root, "mihomo")
		if err := os.WriteFile(binaryPath, []byte("existing"), 0o700); err != nil {
			t.Fatal(err)
		}
		installer := Installer{
			HTTPClient: server.Client(), APIBase: server.URL, Repository: "MetaCubeX/mihomo",
			GOOS: "linux", GOARCH: "amd64", Runner: &recordingRunner{output: []byte("Mihomo Meta v1.19.0")},
			CheckTimeout: 40 * time.Millisecond,
		}
		start := time.Now()
		_, err := installer.Prepare(context.Background(), InstallRequest{
			BinaryPath: binaryPath, CurrentVersion: "v1.19.0",
			DataDir: root, ConfigPath: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging"),
		})
		if elapsed := time.Since(start); elapsed >= time.Second {
			t.Fatalf("Prepare did not fail fast: elapsed=%v", elapsed)
		}
		var apiError protocol.APIError
		if !errors.As(err, &apiError) || apiError.Code != protocol.CodeNetworkFailure {
			t.Fatalf("err=%v", err)
		}
		if !strings.Contains(apiError.Message, "install-aio-remote") {
			t.Fatalf("message=%q missing aio hint", apiError.Message)
		}
	})

	t.Run("same version with valid local binary short-circuits", func(t *testing.T) {
		server := releaseFixture(t, "mihomo-linux-amd64-compatible-v1.19.0.gz", gzipFixture(t, []byte("new-binary")))
		defer server.Close()
		root := t.TempDir()
		binaryPath := filepath.Join(root, "mihomo")
		if err := os.WriteFile(binaryPath, []byte("existing-valid"), 0o700); err != nil {
			t.Fatal(err)
		}
		installer := Installer{
			HTTPClient: server.Client(), APIBase: server.URL, Repository: "MetaCubeX/mihomo",
			GOOS: "linux", GOARCH: "amd64", Runner: &recordingRunner{output: []byte("Mihomo Meta v1.19.0")},
		}
		candidate, err := installer.Prepare(context.Background(), InstallRequest{
			BinaryPath: binaryPath, CurrentVersion: "v1.19.0",
			DataDir: root, ConfigPath: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging"),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer candidate.Cleanup()
		if candidate.Updated() {
			t.Fatal("expected short-circuit, got updated candidate")
		}
		if got, _ := os.ReadFile(binaryPath); string(got) != "existing-valid" {
			t.Fatalf("binary changed=%q (download should not happen)", got)
		}
	})

	t.Run("same version with failing dash-v downloads repair", func(t *testing.T) {
		server := releaseFixture(t, "mihomo-linux-amd64-compatible-v1.19.0.gz", gzipFixture(t, []byte("fresh-binary")))
		defer server.Close()
		root := t.TempDir()
		binaryPath := filepath.Join(root, "mihomo")
		if err := os.WriteFile(binaryPath, []byte("corrupt"), 0o700); err != nil {
			t.Fatal(err)
		}
		installer := Installer{
			HTTPClient: server.Client(), APIBase: server.URL, Repository: "MetaCubeX/mihomo",
			GOOS: "linux", GOARCH: "amd64",
			Runner: &selectiveRunner{existingPath: binaryPath, output: []byte("Mihomo Meta v1.19.0")},
		}
		candidate, err := installer.Prepare(context.Background(), InstallRequest{
			BinaryPath: binaryPath, CurrentVersion: "v1.19.0",
			DataDir: root, ConfigPath: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging"),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer candidate.Cleanup()
		if !candidate.Updated() {
			t.Fatalf("expected download repair, got unmodified candidate")
		}
	})

	t.Run("same version with unparseable dash-v downloads repair", func(t *testing.T) {
		server := releaseFixture(t, "mihomo-linux-amd64-compatible-v1.19.0.gz", gzipFixture(t, []byte("fresh-binary")))
		defer server.Close()
		root := t.TempDir()
		binaryPath := filepath.Join(root, "mihomo")
		if err := os.WriteFile(binaryPath, []byte("weird"), 0o700); err != nil {
			t.Fatal(err)
		}
		installer := Installer{
			HTTPClient: server.Client(), APIBase: server.URL, Repository: "MetaCubeX/mihomo",
			GOOS: "linux", GOARCH: "amd64",
			Runner: &selectiveRunner{existingPath: binaryPath, existingOut: []byte("not-a-version"), output: []byte("Mihomo Meta v1.19.0")},
		}
		candidate, err := installer.Prepare(context.Background(), InstallRequest{
			BinaryPath: binaryPath, CurrentVersion: "v1.19.0",
			DataDir: root, ConfigPath: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging"),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer candidate.Cleanup()
		if !candidate.Updated() {
			t.Fatalf("expected download repair, got unmodified candidate")
		}
	})

	t.Run("new version downloads update", func(t *testing.T) {
		server := releaseFixture(t, "mihomo-linux-amd64-compatible-v1.19.0.gz", gzipFixture(t, []byte("new-mihomo")))
		defer server.Close()
		root := t.TempDir()
		binaryPath := filepath.Join(root, "mihomo")
		if err := os.WriteFile(binaryPath, []byte("old"), 0o700); err != nil {
			t.Fatal(err)
		}
		installer := Installer{
			HTTPClient: server.Client(), APIBase: server.URL, Repository: "MetaCubeX/mihomo",
			GOOS: "linux", GOARCH: "amd64", Runner: &recordingRunner{output: []byte("Mihomo Meta v1.19.0")},
		}
		candidate, err := installer.Prepare(context.Background(), InstallRequest{
			BinaryPath: binaryPath, CurrentVersion: "v1.18.0",
			DataDir: root, ConfigPath: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging"),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer candidate.Cleanup()
		if !candidate.Updated() {
			t.Fatalf("expected update, got unmodified candidate")
		}
	})

	t.Run("corrupt local binary and API 500 fails with aio hint", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()
		root := t.TempDir()
		binaryPath := filepath.Join(root, "mihomo")
		if err := os.WriteFile(binaryPath, []byte("corrupt"), 0o700); err != nil {
			t.Fatal(err)
		}
		installer := Installer{
			HTTPClient: server.Client(), APIBase: server.URL, Repository: "MetaCubeX/mihomo",
			GOOS: "linux", GOARCH: "amd64",
			Runner: &selectiveRunner{existingPath: binaryPath, output: []byte("Mihomo Meta v1.19.0")},
		}
		_, err := installer.Prepare(context.Background(), InstallRequest{
			BinaryPath: binaryPath, CurrentVersion: "v1.19.0",
			DataDir: root, ConfigPath: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging"),
		})
		var apiError protocol.APIError
		if !errors.As(err, &apiError) || apiError.Code != protocol.CodeNetworkFailure {
			t.Fatalf("err=%v", err)
		}
		if !strings.Contains(apiError.Message, "install-aio-remote") {
			t.Fatalf("message=%q missing aio hint", apiError.Message)
		}
	})

	t.Run("no local binary and API timeout fails fast", func(t *testing.T) {
		archive := gzipFixture(t, []byte("payload"))
		server := slowReleaseServer(t, "mihomo-linux-amd64-compatible-v1.19.0.gz", archive, 150*time.Millisecond)
		defer server.Close()
		root := t.TempDir()
		installer := Installer{
			HTTPClient: server.Client(), APIBase: server.URL, Repository: "MetaCubeX/mihomo",
			GOOS: "linux", GOARCH: "amd64", Runner: &recordingRunner{output: []byte("Mihomo Meta v1.19.0")},
			CheckTimeout: 40 * time.Millisecond,
		}
		start := time.Now()
		_, err := installer.Prepare(context.Background(), InstallRequest{
			BinaryPath: filepath.Join(root, "missing"), CurrentVersion: "v1.18.0",
			DataDir: root, ConfigPath: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging"),
		})
		if elapsed := time.Since(start); elapsed >= time.Second {
			t.Fatalf("Prepare did not fail fast: elapsed=%v", elapsed)
		}
		var apiError protocol.APIError
		if !errors.As(err, &apiError) || apiError.Code != protocol.CodeNetworkFailure {
			t.Fatalf("err=%v", err)
		}
	})
}

// selectiveRunner mimics a broken/odd existing binary (existingPath) while
// serving a valid candidate response for any other path (downloaded candidate,
// config validation). existingOut nil → running existingPath errors.
type selectiveRunner struct {
	existingPath string
	existingOut  []byte
	output       []byte
}

func (r *selectiveRunner) Run(_ context.Context, name string, _ ...string) ([]byte, error) {
	if name == r.existingPath {
		if r.existingOut == nil {
			return nil, errors.New("existing binary broken")
		}
		return r.existingOut, nil
	}
	return r.output, nil
}

func slowReleaseServer(t *testing.T, assetName string, archive []byte, delay time.Duration) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		time.Sleep(delay)
		switch request.URL.Path {
		case "/repos/MetaCubeX/mihomo/releases/latest":
			_ = json.NewEncoder(response).Encode(Release{TagName: "v1.19.0", Assets: []Asset{{Name: assetName, URL: server.URL + "/asset", Size: int64(len(archive))}}})
		case "/asset":
			_, _ = response.Write(archive)
		default:
			http.NotFound(response, request)
		}
	}))
	return server
}

func TestLatestReleaseExport(t *testing.T) {
	archive := gzipFixture(t, []byte("payload"))
	server := releaseFixture(t, "mihomo-linux-amd64-compatible-v1.19.0.gz", archive)
	defer server.Close()
	installer := Installer{HTTPClient: server.Client(), APIBase: server.URL, Repository: "MetaCubeX/mihomo"}
	release, err := installer.LatestRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.TagName != "v1.19.0" || len(release.Assets) != 1 {
		t.Fatalf("release=%#v", release)
	}
	if release.Assets[0].Name != "mihomo-linux-amd64-compatible-v1.19.0.gz" {
		t.Fatalf("asset=%#v", release.Assets[0])
	}
}

// TestDownloadExportVerifiesSha256Digest 断言导出的 Download 与原 unexported download 行为
// 一致：落盘字节 + asset.Digest 的 sha256:<hex> 校验。这是 bundler（Task 8）复用、绝不绕过
// 校验的契约（design §4.1）。Download 以 O_WRONLY|O_TRUNC 写入，调用方需先落盘目标文件——
// 与 Prepare 内 CreateTemp 同契约；bundler 自管 staging（design §4.1「解压归 bundler 自管」）。
func TestDownloadExportVerifiesSha256Digest(t *testing.T) {
	payload := []byte("the-core-binary")
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(payload)
	}))
	defer server.Close()
	installer := Installer{HTTPClient: server.Client()}
	dest := filepath.Join(t.TempDir(), "mihomo.gz")
	if err := os.WriteFile(dest, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	asset := Asset{
		Name: "mihomo-linux-amd64-v1.19.0.gz", URL: server.URL,
		Size: int64(len(payload)), Digest: "sha256:" + hex.EncodeToString(sum[:]),
	}
	if err := installer.Download(context.Background(), asset, dest); err != nil {
		t.Fatalf("download err=%v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("dest=%q err=%v", got, err)
	}

	// 摘要不一致必须失败——证明 export 后 Download 仍带 sha256 校验。
	tampered := asset
	tampered.Digest = "sha256:" + strings.Repeat("0", 64)
	if err := installer.Download(context.Background(), tampered, dest); err == nil {
		t.Fatal("expected digest mismatch error, got nil")
	}
}
