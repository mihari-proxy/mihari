package subscription

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCommitRefresh_CatalogCompensationFailureKeepsCachePair(t *testing.T) {
	for _, restored := range []bool{false, true} {
		t.Run(fmt.Sprint(restored), func(t *testing.T) {
			s, _ := newServiceForTest(t, http.NotFoundHandler())
			a, b := t.TempDir(), t.TempDir()
			var pathErr error
			a, pathErr = filepath.EvalSymlinks(a)
			if pathErr != nil {
				t.Fatal(pathErr)
			}
			b, pathErr = filepath.EvalSymlinks(b)
			if pathErr != nil {
				t.Fatal(pathErr)
			}
			oldContent := []byte("proxies: []\nproxy-providers:\n  local: {type: file, path: old.yaml}\n")
			newContent := []byte("proxies: []\nproxy-providers:\n  local: {type: file, path: new.yaml}\n")
			for path, content := range map[string][]byte{filepath.Join(a, "main.yaml"): oldContent, filepath.Join(b, "main.yaml"): newContent} {
				if err := os.WriteFile(path, content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			prepared, err := s.PrepareAdd(context.Background(), "local", localTestURI(filepath.Join(a, "main.yaml")), "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CommitAdd(prepared); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Mutate(func(c *Catalog) error {
				c.Profiles[0].URL = localTestURI(filepath.Join(b, "main.yaml"))
				c.Profiles[0].Version++
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			refresh, err := s.PrepareRefresh(context.Background(), prepared.ProfileID())
			if err != nil {
				t.Fatal(err)
			}
			commitCause, restoreCause := errors.New("post-replacement sync failure"), errors.New("compensation failed before replacement")
			calls := 0
			s.saveCatalog = func(path string, c Catalog) error {
				calls++
				if calls == 1 {
					if err := Save(path, c); err != nil {
						return err
					}
					return commitCause
				}
				if restored {
					if err := Save(path, c); err != nil {
						return err
					}
				}
				return restoreCause
			}
			if _, err := s.CommitRefresh(refresh); !errors.Is(err, commitCause) || !errors.Is(err, restoreCause) {
				t.Fatalf("err=%v", err)
			}
			disk, err := Load(s.catalogPath)
			if err != nil {
				t.Fatal(err)
			}
			cache, err := os.ReadFile(s.CachePath(prepared.ProfileID()))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(s.Snapshot(), disk) {
				t.Fatal("memory and persisted catalog differ after failed compensation")
			}
			expectedBase := a
			if !restored {
				expectedBase = b
			}
			if disk.Profiles[0].CacheBaseDir != expectedBase {
				t.Fatalf("cache base=%q want=%q", disk.Profiles[0].CacheBaseDir, expectedBase)
			}
			expected := oldContent
			if !restored {
				expected = newContent
			}
			if string(cache) != string(expected) {
				t.Fatalf("new catalog paired with old cache: base=%q cache=%q", disk.Profiles[0].CacheBaseDir, cache)
			}
		})
	}
}

func TestCommitAdd_FailedCompensationKeepsPublishedGeneration(t *testing.T) {
	s, _ := newServiceForTest(t, http.NotFoundHandler())
	path := filepath.Join(t.TempDir(), "main.yaml")
	content := []byte("proxies: []\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PrepareAdd(context.Background(), "local", localTestURI(path), "")
	if err != nil {
		t.Fatal(err)
	}
	commitCause, restoreCause := errors.New("committed sync failure"), errors.New("restore failed")
	calls := 0
	s.saveCatalog = func(path string, c Catalog) error {
		calls++
		if calls == 1 {
			if err := Save(path, c); err != nil {
				return err
			}
			return commitCause
		}
		return restoreCause
	}
	if _, err := s.CommitAdd(prepared); !errors.Is(err, commitCause) || !errors.Is(err, restoreCause) {
		t.Fatalf("err=%v", err)
	}
	disk, err := Load(s.catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Snapshot(), disk) {
		t.Fatal("new subscription vanished from live snapshot while committed on disk")
	}
	cache, _, err := s.ReadCache(prepared.ProfileID())
	if err != nil || string(cache) != string(content) {
		t.Fatalf("cache=%q err=%v", cache, err)
	}
}
