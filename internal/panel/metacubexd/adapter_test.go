package metacubexd

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

func TestAdapterResolveLatestUsesGhPagesSHA(t *testing.T) {
	const fullSHA = "8e31c4a9b2c1d0e4f567890abcdef1234567890"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/MetaCubeX/metacubexd/branches/gh-pages" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("missing Accept header")
		}
		var tip release.Branch
		tip.Name = "gh-pages"
		tip.Commit.SHA = fullSHA
		_ = json.NewEncoder(w).Encode(tip)
	}))
	defer server.Close()

	adapter := New(server.Client(), server.URL)
	build, assetURL, err := adapter.ResolveLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if build != fullSHA[:12] {
		t.Fatalf("build=%q want short sha", build)
	}
	if !strings.Contains(assetURL, fullSHA) || !strings.Contains(assetURL, "zipball") {
		t.Fatalf("assetURL=%q", assetURL)
	}
	if adapter.ID() != panel.IDMetaCubeXD || adapter.DisplayName() != "MetaCubeXD" {
		t.Fatalf("id/name mismatch")
	}
}

func TestAdapterResolveLatestRejectsEmptySHA(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(release.Branch{Name: "gh-pages"})
	}))
	defer server.Close()

	if _, _, err := New(server.Client(), server.URL).ResolveLatest(context.Background()); err == nil {
		t.Fatal("expected empty sha rejection")
	}
}

func TestSetupPathHasNoSecretOrController(t *testing.T) {
	path := New(nil, "").SetupPath("127.0.0.1:9191")
	if !strings.Contains(path, "hostname=127.0.0.1") {
		t.Fatalf("path=%q", path)
	}
	lower := strings.ToLower(path)
	for _, forbidden := range []string{"secret", "token", "9090", "controller"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("setup path leaked %q: %s", forbidden, path)
		}
	}
}
