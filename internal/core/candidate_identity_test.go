package core

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareRejectsCandidateVersionMismatch(t *testing.T) {
	for _, channel := range []string{"stable", "alpha"} {
		t.Run(channel, func(t *testing.T) {
			payload := gzipFixture(t, []byte("candidate"))
			var server *httptest.Server
			if channel == "alpha" {
				server = alphaReleaseFixture(t, "mihomo-linux-amd64-alpha-abcdef1.gz", payload, nil)
			} else {
				server = releaseFixture(t, "mihomo-linux-amd64-compatible-v1.19.0.gz", payload)
			}
			defer server.Close()
			root := t.TempDir()
			binary := filepath.Join(root, "mihomo")
			if err := os.WriteFile(binary, []byte("old-core"), 0o700); err != nil {
				t.Fatal(err)
			}
			runner := &recordingRunner{output: []byte("Mihomo Meta v9.99.0")}
			installer := Installer{HTTPClient: server.Client(), APIBase: server.URL, GOOS: "linux", GOARCH: "amd64", Runner: runner}
			candidate, err := installer.Prepare(context.Background(), InstallRequest{BinaryPath: binary, DataDir: root, ConfigPath: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging"), Channel: channel})
			if candidate != nil {
				candidate.Cleanup()
			}
			if err == nil {
				t.Fatal("candidate reported a different version but was accepted")
			}
			if contents, err := os.ReadFile(binary); err != nil || string(contents) != "old-core" {
				t.Fatalf("existing core changed: %q, %v", contents, err)
			}
			if len(runner.args) != 1 {
				t.Fatalf("mismatched candidate reached config execution: %v", runner.args)
			}
		})
	}
}

func TestPrepareDoesNotTrustLocalSelfReportedVersion(t *testing.T) {
	payload := gzipFixture(t, []byte("official-core"))
	server := releaseFixture(t, "mihomo-linux-amd64-compatible-v1.19.0.gz", payload)
	defer server.Close()
	root := t.TempDir()
	binary := filepath.Join(root, "mihomo")
	if err := os.WriteFile(binary, []byte("self-built-core"), 0o700); err != nil {
		t.Fatal(err)
	}
	installer := Installer{HTTPClient: server.Client(), APIBase: server.URL, GOOS: "linux", GOARCH: "amd64", Runner: &recordingRunner{output: []byte("Mihomo Meta v1.19.0")}}
	candidate, err := installer.Prepare(t.Context(), InstallRequest{BinaryPath: binary, DataDir: root, ConfigPath: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging"), CurrentVersion: "v1.19.0", Channel: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.Cleanup()
	warnings, ok := candidate.(interface{ Warnings() []error })
	if !ok || len(warnings.Warnings()) != 1 || !strings.Contains(warnings.Warnings()[0].Error(), "SHA-256") {
		t.Fatal("missing upstream digest did not produce a candidate warning")
	}
	if !candidate.Updated() {
		t.Fatal("explicit official update skipped solely because local core reports latest version")
	}
	if _, err := candidate.Commit(); err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(binary); err != nil || string(contents) != "official-core" {
		t.Fatalf("official candidate not installed: %q, %v", contents, err)
	}
}
