package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestProtectedPrepareUsesOfficialStableAndAlphaWithoutVersionTable(t *testing.T) {
	for _, channel := range []string{"stable", "alpha"} {
		t.Run(channel, func(t *testing.T) {
			store, installer, _ := trustedFixture(t)
			archive := gzipFixture(t, []byte("official new version"))
			version, tag := "v1.20.0", "v1.20.0"
			name := "mihomo-linux-amd64-compatible-v1.20.0.gz"
			if channel == "alpha" {
				version, tag, name = "alpha-abcdef1", "Prerelease-Alpha", "mihomo-linux-amd64-alpha-abcdef1.gz"
			}
			asset := Asset{ID: 456, Name: name, Size: int64(len(archive)), State: "uploaded", UpdatedAt: "2026-09-17T01:00:00Z"}
			requests, executed := 0, 0
			installer.GOOS, installer.GOARCH = "linux", "amd64"
			installer.Executor = configExecutorFunc(func(_ context.Context, command CoreCommand) ([]byte, error) {
				executed++
				if !strings.Contains(command.Binary, "/staging/core/update/") {
					t.Errorf("candidate did not use protected update staging: %q", command.Binary)
				}
				return []byte("Mihomo Meta " + version), nil
			})
			installer.HTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				var value any
				switch {
				case strings.HasSuffix(request.URL.Path, "/releases/latest"), strings.HasSuffix(request.URL.Path, "/releases/tags/Prerelease-Alpha"):
					value = Release{ID: 123, TagName: tag, Assets: []Asset{asset}}
				case strings.HasSuffix(request.URL.Path, "/releases/assets/456"):
					if request.Header.Get("Accept") == "application/octet-stream" {
						return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archive))}, nil
					}
					value = asset
				default:
					return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("not selected"))}, nil
				}
				raw, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})}
			candidate, err := installer.Prepare(t.Context(), InstallRequest{Channel: channel})
			if err != nil {
				t.Fatal(err)
			}
			defer candidate.Cleanup()
			if candidate.Version() != version || requests != 4 || executed != 2 {
				t.Fatalf("version=%q requests=%d commands=%d", candidate.Version(), requests, executed)
			}
			if got, _ := store.Inspect(t.Context(), InstalledBinary, ""); got.Present {
				t.Fatal("prepare installed the candidate")
			}
		})
	}
}
