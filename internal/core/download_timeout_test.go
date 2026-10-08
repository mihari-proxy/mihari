package core

import (
	"net/http"
	"testing"
)

func TestCoreDownloadClientHasNoTimeout(t *testing.T) {
	before := http.DefaultClient.Timeout
	t.Cleanup(func() { http.DefaultClient.Timeout = before })
	first := (Installer{}).httpClient()
	second := (Installer{}).httpClient()
	if first == nil || second == nil || first == http.DefaultClient || second == http.DefaultClient || first == second {
		t.Fatalf("download client first=%p second=%p default=%p", first, second, http.DefaultClient)
	}
	if first.Timeout != 0 || second.Timeout != 0 {
		t.Fatalf("download client timeout = %s", first.Timeout)
	}
	if http.DefaultClient.Timeout != before {
		t.Fatal("default HTTP client timeout changed")
	}
}
