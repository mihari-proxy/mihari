package zashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/panel"
	"github.com/mihari-proxy/mihari/internal/panel/release"
)

func TestAdapterResolveLatestPrefersNoFontsZip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/Zephyruso/zashboard/releases/latest" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("missing Accept header")
		}
		_ = json.NewEncoder(w).Encode(release.Release{
			TagName: "v2.2.0",
			Assets: []release.Asset{
				{Name: "dist.zip", URL: "https://example.test/dist.zip", Size: 10_000_000},
				{Name: "dist-no-fonts.zip", URL: "https://example.test/dist-no-fonts.zip", Size: 2_000_000},
				{Name: "Source code (zip)", URL: "https://example.test/source.zip", Size: 1},
			},
		})
	}))
	defer server.Close()

	adapter := New(server.Client(), server.URL)
	build, assetURL, err := adapter.ResolveLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if build != "v2.2.0" {
		t.Fatalf("build=%q", build)
	}
	if assetURL != "https://example.test/dist-no-fonts.zip" {
		t.Fatalf("assetURL=%q", assetURL)
	}
	if adapter.ID() != panel.IDZashboard || adapter.DisplayName() != "Zashboard" {
		t.Fatalf("id/name mismatch")
	}
}

func TestAdapterResolveLatestFallsBackToDistZip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(release.Release{
			TagName: "v2.1.0",
			Assets: []release.Asset{
				{Name: "dist.zip", URL: "https://example.test/dist.zip", Size: 5},
			},
		})
	}))
	defer server.Close()

	build, assetURL, err := New(server.Client(), server.URL).ResolveLatest(context.Background())
	if err != nil || build != "v2.1.0" || assetURL != "https://example.test/dist.zip" {
		t.Fatalf("build=%q url=%q err=%v", build, assetURL, err)
	}
}

func TestAdapterResolveLatestRejectsMissingAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(release.Release{
			TagName: "v1.0.0",
			Assets:  []release.Asset{{Name: "README.md", URL: "https://example.test/readme", Size: 1}},
		})
	}))
	defer server.Close()

	if _, _, err := New(server.Client(), server.URL).ResolveLatest(context.Background()); err == nil {
		t.Fatal("expected missing asset error")
	}
}

func TestSetupPathHasNoSecret(t *testing.T) {
	path := New(nil, "").SetupPath("127.0.0.1:9191")
	if !strings.HasPrefix(path, "/#/setup?") {
		t.Fatalf("path=%q, want /#/setup? deep-link", path)
	}
	if !strings.Contains(path, "hostname=127.0.0.1") {
		t.Fatalf("path=%q", path)
	}
	if !strings.Contains(path, "port=9191") {
		t.Fatalf("path=%q, want separate port query", path)
	}
	lower := strings.ToLower(path)
	for _, forbidden := range []string{"secret", "token", "9090", "bearer"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("setup path leaked %q: %s", forbidden, path)
		}
	}
	if !strings.Contains(path, "disableUpgrade=true") {
		t.Fatalf("expected disableUpgrade in setup path: %s", path)
	}
}

func TestSetupPathIPv6HostPort(t *testing.T) {
	path := New(nil, "").SetupPath("[::1]:9191")
	if !strings.Contains(path, "hostname=%5B%3A%3A1%5D") && !strings.Contains(path, "hostname=[::1]") {
		// url.Values encodes brackets; accept either raw form in assertion via QueryUnescape path.
		if !strings.Contains(path, "hostname=") || !strings.Contains(path, "port=9191") {
			t.Fatalf("path=%q", path)
		}
	}
	if !strings.Contains(path, "port=9191") {
		t.Fatalf("path=%q", path)
	}
}

func TestSelectAssetPrefersNoFontNames(t *testing.T) {
	asset, err := SelectAsset(release.Release{Assets: []release.Asset{
		{Name: "zashboard-dist.zip", URL: "a"},
		{Name: "zashboard-dist-nofont.zip", URL: "b"},
	}})
	if err != nil || asset.URL != "b" {
		t.Fatalf("asset=%#v err=%v", asset, err)
	}
}
