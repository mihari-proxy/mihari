package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetupCandidate_RevalidationRejectsChangedBytes(t *testing.T) {
	archive := gzipFixture(t, []byte("fake-mihomo-binary"))
	server := releaseFixture(t, "mihomo-linux-amd64-compatible-v1.19.0.gz", archive)
	defer server.Close()
	root := t.TempDir()
	installer := Installer{HTTPClient: server.Client(), APIBase: server.URL, Repository: "MetaCubeX/mihomo", GOOS: "linux", GOARCH: "amd64", Runner: &recordingRunner{output: []byte("Mihomo Meta v1.19.0")}}
	prepared, err := installer.Prepare(t.Context(), InstallRequest{BinaryPath: filepath.Join(root, "mihomo"), DataDir: root, ConfigPath: filepath.Join(root, "config.yaml"), StagingDir: filepath.Join(root, "staging")})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	candidate := prepared.(*Candidate)
	if err := candidate.Revalidate(t.Context(), []byte("proxies: []\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate.path, []byte("replaced-fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Revalidate(t.Context(), []byte("proxies: []\n")); err == nil {
		t.Fatal("changed candidate was accepted")
	}
}
