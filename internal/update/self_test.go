package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSelectSelfAsset(t *testing.T) {
	rel := Release{TagName: "v1.2.3", Assets: []Asset{
		{Name: "mihari-windows-amd64.exe", URL: "u1", Size: 10},
		{Name: "mihari-linux-amd64", URL: "u2", Size: 10},
		{Name: "mihari-linux-amd64.tar.gz", URL: "u3", Size: 10},
	}}
	asset, err := SelectSelfAsset(rel, "windows", "amd64")
	if err != nil || asset.URL != "u1" {
		t.Fatalf("asset=%#v err=%v", asset, err)
	}
	asset, err = SelectSelfAsset(rel, "linux", "amd64")
	if err != nil || asset.URL != "u2" {
		t.Fatalf("asset=%#v err=%v", asset, err)
	}
	if _, err := SelectSelfAsset(rel, "darwin", "arm64"); err == nil {
		t.Fatal("expected missing asset")
	}
}

func TestSelfUpdateDownloadsAndReplaces(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "mihari-test-bin")
	if err := os.WriteFile(binary, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("new-binary-content")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/mihari-proxy/mihari/releases/latest":
			_ = json.NewEncoder(w).Encode(Release{
				TagName: "v9.9.9",
				Assets:  []Asset{{Name: "mihari-linux-amd64", URL: server.URL + "/asset", Size: int64(len(payload))}},
			})
		case "/asset":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	updater := SelfUpdater{
		HTTPClient: server.Client(), APIBase: server.URL,
		GOOS: "linux", GOARCH: "amd64",
	}
	result, err := updater.Update(context.Background(), binary, "v1.0.0")
	if err != nil || !result.Updated || result.Version != "v9.9.9" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	got, err := os.ReadFile(binary)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("binary=%q err=%v", got, err)
	}
}

func TestSelfUpdateSkipsSameVersion(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "mihari")
	if err := os.WriteFile(binary, []byte("same"), 0o755); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{TagName: "v1.0.0", Assets: []Asset{{Name: "mihari-linux-amd64", URL: "x", Size: 1}}})
	}))
	defer server.Close()
	updater := SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL, GOOS: "linux", GOARCH: "amd64"}
	result, err := updater.Update(context.Background(), binary, "v1.0.0")
	if err != nil || result.Updated {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestSelfUpdaterCheckReportsAvailability(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		latest    string
		available bool
	}{
		{name: "new release", current: "v1.0.0", latest: "v1.1.0", available: true},
		{name: "same release", current: "v1.0.0", latest: "v1.0.0", available: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(Release{TagName: test.latest})
			}))
			defer server.Close()

			result, err := (SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL}).Check(context.Background(), test.current)
			if err != nil || result.Current != test.current || result.Latest != test.latest || result.Available != test.available {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestSelfUpdaterCheckDoesNotDownloadAsset(t *testing.T) {
	assetRequests := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/mihari-proxy/mihari/releases/latest":
			_ = json.NewEncoder(w).Encode(Release{
				TagName: "v2.0.0",
				Assets:  []Asset{{Name: "mihari-windows-amd64.exe", URL: server.URL + "/asset", Size: 10}},
			})
		case "/asset":
			assetRequests++
			_, _ = w.Write([]byte("new-binary"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := (SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL}).Check(context.Background(), "v1.0.0")
	if err != nil || !result.Available {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if assetRequests != 0 {
		t.Fatalf("asset requests=%d, want 0", assetRequests)
	}
}
