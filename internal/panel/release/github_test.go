package release

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{TagName: "v1", Assets: []Asset{{Name: "a.zip", URL: "u", Size: 1}}})
	}))
	defer server.Close()
	rel, err := (Client{HTTPClient: server.Client(), APIBase: server.URL}).LatestRelease(context.Background(), "o", "r")
	if err != nil || rel.TagName != "v1" || len(rel.Assets) != 1 {
		t.Fatalf("rel=%#v err=%v", rel, err)
	}
}

func TestClientBranchTip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var tip Branch
		tip.Commit.SHA = "abc123"
		_ = json.NewEncoder(w).Encode(tip)
	}))
	defer server.Close()
	sha, err := (Client{HTTPClient: server.Client(), APIBase: server.URL}).BranchTip(context.Background(), "o", "r", "gh-pages")
	if err != nil || sha != "abc123" {
		t.Fatalf("sha=%q err=%v", sha, err)
	}
}

func TestArchiveURL(t *testing.T) {
	if got := ArchiveURL("", "MetaCubeX", "metacubexd", "gh-pages"); got != "https://github.com/MetaCubeX/metacubexd/archive/refs/heads/gh-pages.zip" {
		t.Fatalf("got=%q", got)
	}
	if got := ArchiveURL("http://127.0.0.1:9", "o", "r", "main"); got != "http://127.0.0.1:9/repos/o/r/zipball/main" {
		t.Fatalf("got=%q", got)
	}
}
