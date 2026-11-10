package subscription

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestReadCacheAtGeneration_RejectsStaleIdentityBeforeReadingBytes(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(ServiceOptions{CatalogPath: filepath.Join(dir, "catalog.yaml"), CacheDir: filepath.Join(dir, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	catalog := Defaults()
	catalog.ActiveID = id
	catalog.Profiles = []Profile{{ID: id, Name: "Synthetic", URL: "https://example.invalid/fixture", Enabled: true, Generation: 2}}
	if err := s.Restore(catalog); err != nil {
		t.Fatal(err)
	}
	// No cache exists: a stale identity must be rejected without attempting IO.
	_, _, err = s.ReadCacheAtGeneration(id, 1)
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("stale identity read the cache: %v", err)
	}
}
