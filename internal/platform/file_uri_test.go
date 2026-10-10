package platform

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFileURI_RoundTripEscapedPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "配置 % space #.yaml")
	uri, err := FileURI(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FileURIPath(uri)
	if err != nil || got != path {
		t.Fatalf("uri=%s got=%s err=%v", uri, got, err)
	}
	canonical, err := FileURI(uri)
	if err != nil || canonical != uri {
		t.Fatalf("canonical=%s err=%v", canonical, err)
	}
}

func TestFileURI_RejectsRelativeOrNonFile(t *testing.T) {
	for _, input := range []string{"local.yaml", "file:local.yaml", "file:///a?query=yes", "file:///a#fragment", "file://user@host/a", "https://example.test/a", "file:///a%00.yaml"} {
		if _, err := FileURI(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestFileURI_CaseInsensitiveSchemeAndLocalhost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.yaml")
	canonical, err := FileURI(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, uri := range []string{strings.Replace(canonical, "file:", "FILE:", 1), strings.Replace(canonical, "file:///", "file://LOCALHOST/", 1)} {
		got, err := FileURI(uri)
		if err != nil || got != canonical {
			t.Fatalf("uri=%s got=%s err=%v", uri, got, err)
		}
	}
}
