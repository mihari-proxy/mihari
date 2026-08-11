package subscription

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
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
		Downloader:  NewDownloader(DownloaderOptions{Client: server.Client()}),
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
		writer.Header().Set("Subscription-Userinfo", "upload=10; download=20; total=100")
		_, _ = writer.Write([]byte("proxies: []\nrules: [MATCH,DIRECT]\n"))
	}))
	profile, err := service.Add("main", url, "")
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
	got := receipt.After.Profiles[0]
	if got.Generation != 1 || got.ETag != `"one"` {
		t.Fatalf("metadata not committed: %#v", got)
	}
	if got.Upload != 10 || got.Download != 20 || got.Total != 100 {
		t.Fatalf("userinfo not committed: %#v", got)
	}
	if _, err := os.Stat(service.CachePath(profile.ID)); err != nil {
		t.Fatal(err)
	}
}

func TestStalePreparedRefreshCannotRecreateDeletedProfile(t *testing.T) {
	service, url := newServiceForTest(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	profile, _ := service.Add("main", url, "")
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
	profile, _ := service.Add("main", url, "")
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

func TestPrepareRefresh_RecordsLastErrorWithoutCachingInvalidBody(t *testing.T) {
	service, url := newServiceForTest(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("not-a-clash-document"))
	}))
	profile, err := service.Add("broken", url, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareRefresh(context.Background(), profile.ID); err == nil {
		t.Fatal("expected prepare failure")
	}
	snap := service.Snapshot()
	if snap.Profiles[0].LastError == "" {
		t.Fatal("last-error was not recorded")
	}
	if _, err := os.Stat(service.CachePath(profile.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid body was cached: %v", err)
	}
}

func TestService_PrepareRefreshFailureNotesLastErrorWithoutDroppingCache(t *testing.T) {
	body := "proxies: []\nrules: [MATCH,DIRECT]\n"
	fail := false
	service, url := newServiceForTest(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if fail {
			_, _ = writer.Write([]byte("not-a-clash-document"))
			return
		}
		_, _ = writer.Write([]byte(body))
	}))
	profile, err := service.Add("main", url, "")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := service.PrepareRefresh(context.Background(), profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CommitRefresh(prepared); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(service.CachePath(profile.ID))
	if err != nil {
		t.Fatal(err)
	}
	fail = true
	if _, err := service.PrepareRefresh(context.Background(), profile.ID); err == nil {
		t.Fatal("expected prepare failure")
	}
	snap := service.Snapshot()
	if snap.Profiles[0].LastError == "" {
		t.Fatal("last-error was not recorded")
	}
	after, err := os.ReadFile(service.CachePath(profile.ID))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("valid cache was changed on failed refresh")
	}
}

func TestRollbackRefreshRestoresCatalogAndCache(t *testing.T) {
	body := "proxies: []\n"
	service, url := newServiceForTest(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(body))
	}))
	profile, _ := service.Add("main", url, "")
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

type recordingFetcher struct {
	last FetchRequest
	give FetchResult
}

func (r *recordingFetcher) Fetch(_ context.Context, request FetchRequest) (FetchResult, error) {
	r.last = request
	return r.give, nil
}

func openServiceWithFetcher(t *testing.T, fetcher Fetcher) *Service {
	t.Helper()
	root := t.TempDir()
	service, err := Open(ServiceOptions{
		CatalogPath: filepath.Join(root, "catalog.yaml"),
		CacheDir:    filepath.Join(root, "cache"),
		Downloader:  fetcher,
		Now:         func() time.Time { return time.Unix(500, 0).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestAddPersistsProxyMode(t *testing.T) {
	service := openServiceWithFetcher(t, &recordingFetcher{})
	profile, err := service.Add("main", "https://example.test/sub", ProxyModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if profile.ProxyMode != ProxyModeAuto {
		t.Fatalf("Add did not persist proxy mode: %q", profile.ProxyMode)
	}
	// The persisted catalog should also carry the value after a fresh load.
	loaded, err := Load(service.catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Profiles[0].ProxyMode != ProxyModeAuto {
		t.Fatalf("catalog did not persist proxy mode: %q", loaded.Profiles[0].ProxyMode)
	}
}

func TestPrepareRefreshPassesProxyMode(t *testing.T) {
	recorder := &recordingFetcher{give: FetchResult{Content: []byte("proxies: []\nrules: [MATCH,DIRECT]\n")}}
	service := openServiceWithFetcher(t, recorder)
	profile, err := service.Add("main", "https://example.test/sub", ProxyModeProxy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareRefresh(context.Background(), profile.ID); err != nil {
		t.Fatal(err)
	}
	if recorder.last.Mode != ProxyModeProxy {
		t.Fatalf("PrepareRefresh passed Mode=%q want %q", recorder.last.Mode, ProxyModeProxy)
	}
}

// TestOpenProxyAddrRoutesFetchThroughProxy verifies the assembly chain the feature
// depends on: a host:port ProxyAddr is turned into a real proxy-capable downloader,
// so a proxy-mode refresh actually reaches the proxy instead of silently going direct.
func TestOpenProxyAddrRoutesFetchThroughProxy(t *testing.T) {
	body := []byte("proxies: []\nrules: [MATCH,DIRECT]\n")
	directServer, proxyServer, directHits, proxyHits := proxyCountingServers(t, body)
	root := t.TempDir()
	service, err := Open(ServiceOptions{
		CatalogPath: filepath.Join(root, "catalog.yaml"),
		CacheDir:    filepath.Join(root, "cache"),
		ProxyAddr:   mustProxyURL(t, proxyServer.URL).Host,
		Now:         func() time.Time { return time.Unix(500, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	profile, err := service.Add("main", directServer.URL, ProxyModeProxy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareRefresh(context.Background(), profile.ID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := atomic.LoadInt32(proxyHits); got != 1 {
		t.Fatalf("ProxyAddr should route proxy-mode fetch through it: proxyHits=%d", got)
	}
	if got := atomic.LoadInt32(directHits); got != 0 {
		t.Fatalf("proxy mode should not reach direct: directHits=%d", got)
	}
}
