package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLatestVersion_MetadataOnly(t *testing.T) {
	for _, tc := range []struct{ channel, tag, asset, want string }{
		{"stable", "v1.20.0", "mihomo-linux-amd64-v1.20.0.gz", "v1.20.0"},
		{"alpha", "Prerelease-Alpha", "mihomo-linux-amd64-alpha-abc1234.gz", "alpha-abc1234"},
	} {
		t.Run(tc.channel, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				path := "/repos/MetaCubeX/mihomo/releases/latest"
				if tc.channel == "alpha" {
					path = "/repos/MetaCubeX/mihomo/releases/tags/Prerelease-Alpha"
				}
				if r.URL.Path != path {
					t.Errorf("unexpected request %s", r.URL.Path)
				}
				if err := json.NewEncoder(w).Encode(Release{TagName: tc.tag, Assets: []Asset{{Name: tc.asset}}}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			installer := Installer{HTTPClient: server.Client(), APIBase: server.URL, GOOS: "linux", GOARCH: "amd64"}
			checker, ok := any(installer).(interface {
				LatestVersion(context.Context, string) (string, error)
			})
			if !ok {
				t.Fatal("installer does not support read-only version checks")
			}
			got, err := checker.LatestVersion(t.Context(), tc.channel)
			if err != nil || got != tc.want || calls != 1 {
				t.Fatalf("got %q, err %v, requests %d", got, err, calls)
			}
		})
	}
}

func TestLatestVersion_PropagatesCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("canceled check reached upstream") }))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := (Installer{HTTPClient: server.Client(), APIBase: server.URL}).LatestVersion(ctx, "stable")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}
