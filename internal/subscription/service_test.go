package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newServiceForTest(t *testing.T, handler http.Handler) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	service, err := Open(ServiceOptions{
		CatalogPath: filepath.Join(root, "catalog.yaml"),
		CacheDir:    filepath.Join(root, "cache"),
		Downloader:  NewDownloader(server.Client()),
		Now:         func() time.Time { return time.Unix(500, 0).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, server.URL
}

func TestPrepareRefreshDoesNotPersistUntilCommit(t *testing.T) {
	service, url := newServiceForTest(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("ETag", `"one"`)
		_, _ = writer.Write([]byte("proxies: []\nrules: [MATCH,DIRECT]\n"))
	}))
	profile, err := service.Add("main", url)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := service.PrepareRefresh(context.Background(), profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(service.CachePath(profile.ID)); !os.IsNotExist(err) {
		t.Fatalf("prepare wrote cache: %v", err)
	}
	receipt, err := service.CommitRefresh(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.After.Profiles[0].Generation != 1 || receipt.After.Profiles[0].ETag != `"one"` {
		t.Fatalf("metadata not committed: %#v", receipt.After.Profiles[0])
	}
	if _, err := os.Stat(service.CachePath(profile.ID)); err != nil {
		t.Fatal(err)
	}
}

func TestStalePreparedRefreshCannotRecreateDeletedProfile(t *testing.T) {
	service, url := newServiceForTest(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	profile, _ := service.Add("main", url)
	prepared, err := service.PrepareRefresh(context.Background(), profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Mutate(func(catalog *Catalog) error {
		catalog.Profiles = nil
		catalog.ActiveID = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CommitRefresh(prepared); err == nil {
		t.Fatal("stale refresh unexpectedly committed")
	}
	if _, err := os.Stat(service.CachePath(profile.ID)); !os.IsNotExist(err) {
		t.Fatalf("deleted cache was recreated: %v", err)
	}
}

func TestNotModifiedRetainsCacheAndAdvancesMetadataVersion(t *testing.T) {
	requests := 0
	service, url := newServiceForTest(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if requests == 1 {
			writer.Header().Set("ETag", `"one"`)
			_, _ = writer.Write([]byte("proxies: []\n"))
			return
		}
		if request.Header.Get("If-None-Match") != `"one"` {
			t.Errorf("missing etag")
		}
		writer.WriteHeader(http.StatusNotModified)
	}))
	profile, _ := service.Add("main", url)
	first, _ := service.PrepareRefresh(context.Background(), profile.ID)
	firstReceipt, err := service.CommitRefresh(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.PrepareRefresh(context.Background(), profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondReceipt, err := service.CommitRefresh(second)
	if err != nil {
		t.Fatal(err)
	}
	if secondReceipt.After.Profiles[0].Generation != firstReceipt.After.Profiles[0].Generation {
		t.Fatal("304 changed cache generation")
	}
	if secondReceipt.After.Profiles[0].Version <= firstReceipt.After.Profiles[0].Version {
		t.Fatal("304 did not advance metadata version")
	}
}

func TestRollbackRefreshRestoresCatalogAndCache(t *testing.T) {
	body := "proxies: []\n"
	service, url := newServiceForTest(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(body))
	}))
	profile, _ := service.Add("main", url)
	first, _ := service.PrepareRefresh(context.Background(), profile.ID)
	_, _ = service.CommitRefresh(first)
	body = "proxies: [{name: changed}]\n"
	second, _ := service.PrepareRefresh(context.Background(), profile.ID)
	receipt, err := service.CommitRefresh(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Rollback(receipt); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(service.CachePath(profile.ID))
	if string(content) != "proxies: []\n" {
		t.Fatalf("cache not restored: %q", content)
	}
}
