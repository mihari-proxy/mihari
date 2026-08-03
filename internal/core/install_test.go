package core

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
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
