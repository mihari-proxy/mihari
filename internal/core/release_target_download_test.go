package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadTargetFreezesAssetAndRechecksMetadata(t *testing.T) {
	for _, scenario := range []string{"unchanged", "no digest", "replaced", "missing", "digest mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			payload := []byte("selected-alpha-asset")
			sum := sha256.Sum256(payload)
			asset := Asset{ID: 456, Name: "mihomo-linux-amd64-alpha-abcdef1.gz", Size: int64(len(payload)), State: "uploaded", UpdatedAt: "2026-09-17T01:00:00Z", Digest: "sha256:" + hex.EncodeToString(sum[:])}
			if scenario == "no digest" {
				asset.Digest = ""
			}
			if scenario == "digest mismatch" {
				asset.Digest = "sha256:" + strings.Repeat("a", 64)
			}
			target := ReleaseTarget{repository: "MetaCubeX/mihomo", channel: "alpha", tag: "Prerelease-Alpha", releaseID: 123, asset: asset, goos: "linux", goarch: "amd64"}
			metadataReads, downloads := 0, 0
			installer := Installer{HTTPClient: &http.Client{Transport: diagnosticRoundTripper(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() != "https://api.github.com/repos/MetaCubeX/mihomo/releases/assets/456" {
					t.Errorf("target changed or latest re-resolved: %s", request.URL)
				}
				if request.Header.Get("Accept") == "application/octet-stream" {
					downloads++
					return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(payload)), Header: make(http.Header)}, nil
				}
				metadataReads++
				observed := asset
				if downloads > 0 && scenario == "replaced" {
					observed.UpdatedAt = "2026-09-17T02:00:00Z"
				}
				if downloads > 0 && scenario == "missing" {
					return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("removed")), Header: make(http.Header)}, nil
				}
				raw, err := json.Marshal(observed)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
			})}}
			destination := filepath.Join(t.TempDir(), "archive")
			if err := os.WriteFile(destination, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			err := installer.downloadTarget(t.Context(), target, destination)
			wantSuccess := scenario == "unchanged" || scenario == "no digest"
			if (err == nil) != wantSuccess {
				t.Fatalf("err=%v, expected success=%v", err, wantSuccess)
			}
			if wantSuccess {
				if metadataReads != 2 || downloads != 1 {
					t.Fatalf("metadata reads=%d downloads=%d", metadataReads, downloads)
				}
				if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, payload) {
					t.Fatalf("download=%q err=%v", got, err)
				}
			}
		})
	}
}

func TestPrepareRechecksTargetBeforeExecution(t *testing.T) {
	payload := gzipFixture(t, []byte("candidate"))
	asset := Asset{ID: 456, Name: "mihomo-linux-amd64-compatible-v1.20.0.gz", Size: int64(len(payload)), State: "uploaded", UpdatedAt: "2026-09-17T01:00:00Z", URL: "https://github.com/MetaCubeX/mihomo/releases/download/v1.20.0/core.gz"}
	downloaded := false
	runner := &recordingRunner{output: []byte("Mihomo Meta v1.20.0")}
	installer := Installer{GOOS: "linux", GOARCH: "amd64", Runner: runner, HTTPClient: &http.Client{Transport: diagnosticRoundTripper(func(request *http.Request) (*http.Response, error) {
		var value any
		switch {
		case strings.HasSuffix(request.URL.Path, "/releases/latest"):
			value = Release{ID: 123, TagName: "v1.20.0", Assets: []Asset{asset}}
		case request.Header.Get("Accept") == "application/octet-stream" || request.URL.Host == "github.com":
			downloaded = true
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(payload)), Header: make(http.Header)}, nil
		default:
			observed := asset
			if downloaded {
				observed.UpdatedAt = "2026-09-17T02:00:00Z"
			}
			value = observed
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
	})}}
	root := t.TempDir()
	binary := filepath.Join(root, "mihomo")
	if err := os.WriteFile(binary, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	candidate, err := installer.Prepare(t.Context(), InstallRequest{BinaryPath: binary, DataDir: root, ConfigPath: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging")})
	if candidate != nil {
		candidate.Cleanup()
	}
	if err == nil || len(runner.args) != 0 {
		t.Fatalf("changed target reached execution: err=%v args=%v", err, runner.args)
	}
	if got, err := os.ReadFile(binary); err != nil || string(got) != "old" {
		t.Fatalf("old core changed: %q err=%v", got, err)
	}
}

func TestDownloadTargetRestrictsRedirects(t *testing.T) {
	for _, tt := range []struct {
		location string
		allowed  bool
	}{
		{"https://release-assets.githubusercontent.com/github-production-release-asset/payload", true},
		{"https://objects.githubusercontent.com/payload", true},
		{"http://release-assets.githubusercontent.com/payload", false},
		{"https://attacker.invalid/payload", false},
		{"https://api.github.com.attacker.invalid/payload", false},
		{"https://user:pass@github.com/payload", false},
		{"https://github.com:444/payload", false},
	} {
		t.Run(tt.location, func(t *testing.T) {
			payload := []byte("payload")
			asset := Asset{ID: 456, Name: "mihomo-linux-arm64-v1.20.0.gz", Size: int64(len(payload)), State: "uploaded", UpdatedAt: "2026-09-17T01:00:00Z"}
			target := ReleaseTarget{repository: "MetaCubeX/mihomo", releaseID: 123, asset: asset}
			redirectHits := 0
			installer := Installer{HTTPClient: &http.Client{Transport: diagnosticRoundTripper(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() == tt.location {
					redirectHits++
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(payload))}, nil
				}
				if request.Header.Get("Accept") == "application/octet-stream" {
					return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{tt.location}}, Body: io.NopCloser(strings.NewReader(""))}, nil
				}
				raw, err := json.Marshal(asset)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})}}
			destination := filepath.Join(t.TempDir(), "archive")
			if err := os.WriteFile(destination, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			err := installer.downloadTarget(t.Context(), target, destination)
			if (err == nil) != tt.allowed || (!tt.allowed && redirectHits != 0) {
				t.Fatalf("allowed=%v err=%v redirect hits=%d", tt.allowed, err, redirectHits)
			}
		})
	}
}
