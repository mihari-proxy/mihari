package subscription

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func localTestURI(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func TestLocalSource_RejectsDirectoryOversizeAndCancellation(t *testing.T) {
	s, _ := newServiceForTest(t, http.NotFoundHandler())
	dir := t.TempDir()
	path := filepath.Join(dir, "large.yaml")
	if err := os.WriteFile(path, make([]byte, maxDocumentBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, path} {
		if _, err := s.PrepareAdd(context.Background(), "local", localTestURI(path), ""); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.PrepareAdd(ctx, "cancelled", localTestURI(path), "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if len(s.Snapshot().Profiles) != 0 {
		t.Fatal("failed preparation persisted")
	}
}

func TestLocalSource_SymlinkRefreshPreservesSavedDirectory(t *testing.T) {
	s, _ := newServiceForTest(t, http.NotFoundHandler())
	a, b := t.TempDir(), t.TempDir()
	a, err := filepath.EvalSymlinks(a)
	if err != nil {
		t.Fatal(err)
	}
	b, err = filepath.EvalSymlinks(b)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("proxies: []\nproxy-providers:\n  nodes: {type: file, path: nodes.yaml}\n")
	for _, dir := range []string{a, b} {
		if err := os.WriteFile(filepath.Join(dir, "main.yaml"), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "source.yaml")
	if err := os.Symlink(filepath.Join(a, "main.yaml"), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	p, err := s.PrepareAdd(context.Background(), "local", localTestURI(link), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitAdd(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(b, "main.yaml"), link); err != nil {
		t.Fatal(err)
	}
	_, doc, err := s.ReadCache(p.ProfileID())
	if err != nil {
		t.Fatal(err)
	}
	providers, _ := referenceMap(doc["proxy-providers"])
	nodes, _ := referenceMap(providers["nodes"])
	if nodes["path"] != filepath.Join(a, "nodes.yaml") {
		t.Fatalf("cached base changed: %v", nodes["path"])
	}
	refresh, err := s.PrepareRefresh(context.Background(), p.ProfileID())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.CommitRefresh(refresh)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.After.Profiles[0].CacheBaseDir != b {
		t.Fatalf("base=%s", receipt.After.Profiles[0].CacheBaseDir)
	}
	if err := s.Rollback(receipt); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Profiles[0].CacheBaseDir != a {
		t.Fatal("rollback lost cached base directory")
	}
}

func TestCatalog_LocalSourceAccepted(t *testing.T) {
	catalog := Defaults()
	catalog.Profiles = []Profile{{ID: "0123456789abcdef0123456789abcdef", Name: "Local", URL: localTestURI(filepath.Join(t.TempDir(), "配置 space.yaml")), Enabled: true}}
	if err := catalog.Normalize(); err != nil {
		t.Fatalf("local source rejected: %v", err)
	}
}

func TestPrepareRefresh_LocalSourceRetainsOriginalCache(t *testing.T) {
	s, _ := newServiceForTest(t, http.NotFoundHandler())
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte("proxies: []\nmode: direct\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := s.Add("Local", localTestURI(path), "")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PrepareRefresh(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitRefresh(prepared); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.ReadCache(p.ID)
	if err != nil || string(got) != string(content) {
		t.Fatalf("cache=%q err=%v", got, err)
	}
	if _, err := s.PrepareRefresh(context.Background(), p.ID); err == nil {
		t.Fatal("missing source refresh succeeded")
	}
	got, _, err = s.ReadCache(p.ID)
	if err != nil || string(got) != string(content) {
		t.Fatalf("failed refresh replaced cache: %q %v", got, err)
	}
}

func TestCommitAdd_CatalogPostCommitFailureRestoresDisk(t *testing.T) {
	s, source := newServiceForTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	prepared, err := s.PrepareAdd(context.Background(), "new", source, "")
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("catalog directory sync failed after replacement")
	calls := 0
	s.saveCatalog = func(path string, catalog Catalog) error {
		calls++
		if err := Save(path, catalog); err != nil {
			return err
		}
		if calls == 1 {
			return cause
		}
		return nil
	}
	if _, err := s.CommitAdd(prepared); !errors.Is(err, cause) {
		t.Fatalf("err=%v", err)
	}
	disk, err := Load(s.catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(disk.Profiles) != 0 || len(s.Snapshot().Profiles) != 0 {
		t.Fatalf("disk=%+v memory=%+v", disk, s.Snapshot())
	}
	if _, err := os.Stat(s.CachePath(prepared.ProfileID())); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache err=%v", err)
	}
}
