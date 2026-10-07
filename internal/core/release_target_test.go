package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveTargetAcceptsOfficialMetadataAndOptionalDigest(t *testing.T) {
	for _, digest := range []string{"", "sha256:" + strings.Repeat("a", 64)} {
		t.Run(digest, func(t *testing.T) {
			metadata := targetMetadata()
			metadata["digest"] = digest
			installer := targetInstaller(t, metadata)
			target, err := installer.ResolveTarget(t.Context(), "stable")
			if err != nil {
				t.Fatal(err)
			}
			if target.releaseID != 123 || target.asset.ID != 456 || target.tag != "v1.20.0" || target.asset.Digest != digest || target.channel != "stable" {
				t.Fatalf("target=%+v", target)
			}
		})
	}
}

func TestResolveTargetRejectsInvalidMetadata(t *testing.T) {
	for _, tt := range []struct {
		field string
		value any
	}{
		{"id", 0}, {"id", -1}, {"state", "new"}, {"state", ""},
		{"size", 0}, {"size", maxCoreArchiveSize + 1},
		{"digest", "sha256:bad"}, {"digest", "md5:" + strings.Repeat("a", 32)},
		{"digest", "sha256:" + strings.Repeat("z", 64)}, {"updated_at", "not-a-time"},
	} {
		t.Run(tt.field+"/"+fmt.Sprint(tt.value), func(t *testing.T) {
			metadata := targetMetadata()
			metadata[tt.field] = tt.value
			if _, err := targetInstaller(t, metadata).ResolveTarget(t.Context(), "stable"); err == nil {
				t.Fatalf("accepted invalid metadata: %v=%v", tt.field, tt.value)
			}
		})
	}
}

func targetMetadata() map[string]any {
	return map[string]any{"id": 456, "name": "mihomo-linux-amd64-compatible-v1.20.0.gz", "size": 32, "state": "uploaded", "updated_at": "2026-09-17T01:00:00Z", "digest": ""}
}

func targetInstaller(t *testing.T, metadata map[string]any) Installer {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/MetaCubeX/mihomo/releases/latest" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"id": 123, "tag_name": "v1.20.0", "assets": []any{metadata}}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return Installer{HTTPClient: server.Client(), APIBase: server.URL, GOOS: "linux", GOARCH: "amd64"}
}
