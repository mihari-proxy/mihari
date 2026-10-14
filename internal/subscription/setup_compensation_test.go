package subscription

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestSetupCommit_CatalogFailureRestoresWholeStep(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		for _, rollbackFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("refresh=%v/rollbackFails=%v", refresh, rollbackFails), func(t *testing.T) {
				s, url := newServiceForTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
				seed, err := s.PrepareAdd(t.Context(), "B", url, "")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.CommitAdd(seed); err != nil {
					t.Fatal(err)
				}
				before := s.Snapshot()
				oldCache, err := os.ReadFile(s.CachePath(seed.ProfileID()))
				if err != nil {
					t.Fatal(err)
				}
				add, err := s.PrepareAdd(t.Context(), "A", url, "")
				if err != nil {
					t.Fatal(err)
				}
				prepared, err := s.PrepareRefresh(t.Context(), seed.ProfileID())
				if err != nil {
					t.Fatal(err)
				}
				prepared.result.Content = []byte("proxies: []\nrules: [MATCH,REJECT]\n")
				commitErr, restoreErr := errors.New("committed sync failure"), errors.New("restore failed")
				calls := 0
				s.saveCatalog = func(path string, c Catalog) error {
					calls++
					if calls == 1 {
						if err := Save(path, c); err != nil {
							return err
						}
						return commitErr
					}
					if calls == 2 || rollbackFails {
						return restoreErr
					}
					return Save(path, c)
				}
				if refresh {
					_, err = s.CommitRefreshAndActivate(prepared)
				} else {
					_, err = s.CommitAddAndActivate(add)
				}
				if !errors.Is(err, commitErr) || !errors.Is(err, restoreErr) {
					t.Fatalf("lost causes: %v", err)
				}
				var api protocol.APIError
				degraded := errors.As(err, &api) && api.Details["degraded"] == true
				if degraded != rollbackFails {
					t.Fatalf("degraded=%v want=%v err=%v", degraded, rollbackFails, err)
				}
				disk, loadErr := Load(s.catalogPath)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				if !reflect.DeepEqual(disk, s.Snapshot()) {
					t.Fatal("disk and memory differ")
				}
				if rollbackFails {
					id := add.ProfileID()
					expected := add.refresh.result.Content
					if refresh {
						id = seed.ProfileID()
						expected = prepared.result.Content
					}
					cache, err := os.ReadFile(s.CachePath(id))
					if err != nil || string(cache) != string(expected) {
						t.Fatalf("retained catalog/cache mismatch: %q %v", cache, err)
					}
				}
				if !rollbackFails {
					if !reflect.DeepEqual(before, disk) {
						t.Fatalf("step left changed catalog: %+v", disk)
					}
					cache, err := os.ReadFile(s.CachePath(seed.ProfileID()))
					if err != nil || string(cache) != string(oldCache) {
						t.Fatalf("cache=%q err=%v", cache, err)
					}
					if _, err := os.Stat(s.CachePath(add.ProfileID())); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("new cache retained: %v", err)
					}
				}
			})
		}
	}
}

func TestSetupRefresh_ChangedNotModifiedCacheIsPreparedAgain(t *testing.T) {
	s, url := newServiceForTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	add, err := s.PrepareAdd(t.Context(), "A", url, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitAdd(add); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PrepareRefresh(t.Context(), add.ProfileID())
	if err != nil {
		t.Fatal(err)
	}
	// Model a conditional response tied to the original cache generation.
	prepared.result.NotModified = true
	newer := prepared
	newer.result.NotModified = false
	newer.result.Content = []byte("proxies: []\nrules: [MATCH,REJECT]\n")
	if _, err = s.CommitRefresh(newer); err != nil {
		t.Fatal(err)
	}
	next, err := s.ReprepareSetupRefresh(t.Context(), prepared)
	if err != nil || next.result.NotModified || next.profileVersion == prepared.profileVersion {
		t.Fatalf("stale conditional response reused: %+v %v", next, err)
	}
}

func TestSetupCommit_CacheCompensationFailureIsDegraded(t *testing.T) {
	s, url := newServiceForTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	prepared, err := s.PrepareAdd(t.Context(), "A", url, "")
	if err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("cache replacement failed")
	s.writeCache = func(path string, _ []byte, _ os.FileMode) error {
		if err := os.Mkdir(path, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(path, "blocked"), []byte("fixture"), 0600); err != nil {
			return err
		}
		return writeErr
	}
	_, err = s.CommitAddAndActivate(prepared)
	var api protocol.APIError
	if !errors.Is(err, writeErr) || !errors.As(err, &api) || api.Details["degraded"] != true {
		t.Fatalf("compensation failure hidden by cache error: %v", err)
	}
	if len(s.Snapshot().Profiles) != 0 {
		t.Fatal("failed add was published")
	}
}

func TestSetupCommit_CacheRollbackFailureKeepsMatchingGeneration(t *testing.T) {
	for _, cacheRestored := range []bool{false, true} {
		for _, catalogOutcome := range []string{"success", "before-replace", "after-replace"} {
			for _, sameBytes := range []bool{false, true} {
				t.Run(fmt.Sprintf("cacheRestored=%v/catalog=%s/sameBytes=%v", cacheRestored, catalogOutcome, sameBytes), func(t *testing.T) {
					s, _ := newServiceForTest(t, http.NotFoundHandler())
					oldBase, newBase := t.TempDir(), t.TempDir()
					// Equal bytes still have different meanings when local reference bases differ.
					oldContent := []byte("proxies: []\nproxy-providers:\n  local: {type: file, path: nodes.yaml}\n")
					newContent := oldContent
					if !sameBytes {
						newContent = append(append([]byte(nil), oldContent...), []byte("rules: [MATCH,REJECT]\n")...)
					}
					for path, content := range map[string][]byte{filepath.Join(oldBase, "main.yaml"): oldContent, filepath.Join(newBase, "main.yaml"): newContent} {
						if err := os.WriteFile(path, content, 0600); err != nil {
							t.Fatal(err)
						}
					}
					add, err := s.PrepareAdd(t.Context(), "local", localTestURI(filepath.Join(oldBase, "main.yaml")), "")
					if err != nil {
						t.Fatal(err)
					}
					if _, err = s.CommitAdd(add); err != nil {
						t.Fatal(err)
					}
					if _, _, err = s.Mutate(func(c *Catalog) error {
						c.Profiles[0].URL = localTestURI(filepath.Join(newBase, "main.yaml"))
						c.Profiles[0].Version++
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					before := s.Snapshot()
					prepared, err := s.PrepareRefresh(t.Context(), add.ProfileID())
					if err != nil {
						t.Fatal(err)
					}
					commitErr, restoreErr, cacheErr, catalogErr := errors.New("catalog committed but sync failed"), errors.New("first catalog restoration failed"), errors.New("cache rollback failed"), errors.New("final catalog rollback failed")
					var after Catalog
					saves := 0
					s.saveCatalog = func(path string, c Catalog) error {
						saves++
						if saves == 1 {
							after = c.Clone()
							if err := Save(path, c); err != nil {
								return err
							}
							return commitErr
						}
						if saves == 2 {
							return restoreErr
						}
						if catalogOutcome == "before-replace" {
							return catalogErr
						}
						if err := Save(path, c); err != nil {
							return err
						}
						if catalogOutcome == "after-replace" {
							return catalogErr
						}
						return nil
					}
					s.restoreCacheFile = func(path string, b []byte, mode os.FileMode) (config.CommitResult, error) {
						if cacheRestored {
							result, err := config.AtomicWriteWithCommit(path, b, mode)
							if err != nil {
								return result, err
							}
							result.Warning = errors.Join(result.Warning, cacheErr)
							return result, nil
						}
						return config.CommitResult{}, cacheErr
					}
					_, err = s.CommitRefreshAndActivate(prepared)
					var api protocol.APIError
					if !errors.Is(err, commitErr) || !errors.Is(err, restoreErr) || !errors.Is(err, cacheErr) || !errors.As(err, &api) || api.Details["degraded"] != true {
						t.Fatalf("lost recovery failure: %v", err)
					}
					if cacheRestored && catalogOutcome != "success" && !errors.Is(err, catalogErr) {
						t.Fatalf("lost catalog rollback failure: %v", err)
					}
					expected, expectedContent := after, newContent
					if cacheRestored && catalogOutcome != "before-replace" {
						expected, expectedContent = before, oldContent
					}
					disk, loadErr := Load(s.catalogPath)
					if loadErr != nil {
						t.Fatal(loadErr)
					}
					if !reflect.DeepEqual(disk, expected) || !reflect.DeepEqual(s.Snapshot(), expected) {
						t.Fatalf("wrong cache generation/base: disk=%+v expected=%+v", disk, expected)
					}
					cached, doc, readErr := s.ReadCache(add.ProfileID())
					if readErr != nil {
						t.Fatal(readErr)
					}
					if string(cached) != string(expectedContent) {
						t.Fatalf("cache mismatches surviving catalog: %q", cached)
					}
					refs := FileReferences(doc)
					expectedPath := filepath.Join(expected.Profiles[0].CacheBaseDir, "nodes.yaml")
					if len(refs) != 1 || refs[0].Path != expectedPath {
						t.Fatalf("local reference resolved against wrong base: %+v want=%s", refs, expectedPath)
					}
				})
			}
		}
	}
}
