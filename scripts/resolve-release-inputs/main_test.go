package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/scripts/internal/releaseinputs"
)

const fixtureCommit = "0123456789abcdef0123456789abcdef01234567"

type resolverFixture struct {
	server       *httptest.Server
	assets       []fixtureAsset
	requestsMu   sync.Mutex
	requests     []string
	override     func(http.ResponseWriter, *http.Request) bool
	releaseEdit  func(*core.Release)
	commitSHA    string
	commitType   string
	blockPayload string
	payloadStart chan struct{}
}

type fixtureAsset struct {
	platform string
	id       int64
	name     string
	payload  string
	digest   string
}

func newResolverFixture(t *testing.T) *resolverFixture {
	t.Helper()
	fixture := &resolverFixture{
		commitSHA:    fixtureCommit,
		commitType:   "commit",
		payloadStart: make(chan struct{}),
		assets: []fixtureAsset{
			{platform: "darwin/amd64", id: 101, name: "mihomo-darwin-amd64-compatible-v1.19.30.gz", payload: "darwin-amd64", digest: "5fc18e80e71334e0ced23b751a1b626ad0a806e097ba20202032db5e05923e3f"},
			{platform: "darwin/arm64", id: 102, name: "mihomo-darwin-arm64-v1.19.30.gz", payload: "darwin-arm64", digest: "1a349b12b50ad5b43740e0952adc33c7805ce06f091074be977624d09ed9d432"},
			{platform: "linux/amd64", id: 103, name: "mihomo-linux-amd64-compatible-v1.19.30.gz", payload: "linux-amd64", digest: "abb35c616421af72198ad7c2aeeef38516f08f6a7afb2a728cf0068a8a712ddc"},
			{platform: "linux/arm64", id: 104, name: "mihomo-linux-arm64-v1.19.30.gz", payload: "linux-arm64", digest: "17d46d4991b2edd5e445342a72ba0cb7cf09e4849b5e98c16408ce11e05c7388"},
			{platform: "windows/amd64", id: 105, name: "mihomo-windows-amd64-compatible-v1.19.30.zip", payload: "windows-amd64", digest: "b5c168db719e3a66397ce14e1907340ebdb78b910c98df897c665a1a74804190"},
			{platform: "windows/arm64", id: 106, name: "mihomo-windows-arm64-v1.19.30.zip", payload: "windows-arm64", digest: "604d41bacaf2d2d45aa875439af0ee31dc84692ae22365e6111e38a3310c1c01"},
		},
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (f *resolverFixture) serveHTTP(response http.ResponseWriter, request *http.Request) {
	f.requestsMu.Lock()
	f.requests = append(f.requests, request.URL.Path)
	f.requestsMu.Unlock()
	if f.override != nil && f.override(response, request) {
		return
	}
	switch request.URL.Path {
	case "/repos/MetaCubeX/mihomo/releases/latest":
		release := core.Release{ID: 77, TagName: "v1.19.30"}
		for _, asset := range f.assets {
			release.Assets = append(release.Assets, core.Asset{
				ID: asset.id, Name: asset.name,
				URL:  "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/" + asset.name,
				Size: int64(len(asset.payload)), Digest: "sha256:" + asset.digest,
			})
		}
		if f.releaseEdit != nil {
			f.releaseEdit(&release)
		}
		_ = json.NewEncoder(response).Encode(release)
	case "/repos/Loyalsoldier/geoip/git/ref/heads/release":
		_ = json.NewEncoder(response).Encode(map[string]any{"object": map[string]string{"type": f.commitType, "sha": f.commitSHA}})
	case "/Loyalsoldier/geoip/" + fixtureCommit + "/GeoLite2-Country.mmdb":
		_, _ = response.Write([]byte("country-mmdb"))
	case "/Loyalsoldier/geoip/" + fixtureCommit + "/GeoLite2-ASN.mmdb":
		_, _ = response.Write([]byte("asn-mmdb"))
	default:
		for _, asset := range f.assets {
			if request.URL.Path == "/MetaCubeX/mihomo/releases/download/v1.19.30/"+asset.name {
				if f.blockPayload == asset.platform {
					close(f.payloadStart)
					<-request.Context().Done()
					return
				}
				_, _ = response.Write([]byte(asset.payload))
				return
			}
		}
		http.NotFound(response, request)
	}
}

func (f *resolverFixture) options(out string) options {
	return options{
		Channel: "stable", Out: out, HTTPClient: f.server.Client(), APIBase: f.server.URL,
		DownloadBase: f.server.URL,
	}
}

func (f *resolverFixture) requestCount(path string) int {
	f.requestsMu.Lock()
	defer f.requestsMu.Unlock()
	count := 0
	for _, request := range f.requests {
		if request == path {
			count++
		}
	}
	return count
}

func TestResolveWritesCanonicalLockForExactlySixPlatforms(t *testing.T) {
	fixture := newResolverFixture(t)
	out := filepath.Join(t.TempDir(), "release-inputs.lock.json")

	options := fixture.options(out)
	if err := resolve(context.Background(), options); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	lock, err := releaseinputs.Load(out)
	if err != nil {
		t.Fatalf("load resolved lock: %v", err)
	}
	if lock.Mihomo.ReleaseID != 77 || lock.Mihomo.Tag != "v1.19.30" {
		t.Fatalf("mihomo release = ID %d tag %q, want 77 v1.19.30", lock.Mihomo.ReleaseID, lock.Mihomo.Tag)
	}
	if len(lock.Mihomo.Assets) != 6 {
		t.Fatalf("asset count = %d, want 6", len(lock.Mihomo.Assets))
	}
	for _, asset := range fixture.assets {
		got, ok := lock.Mihomo.Assets[asset.platform]
		if !ok {
			t.Fatalf("missing platform %q", asset.platform)
		}
		if got.AssetID != asset.id || got.Name != asset.name || got.SHA256 != asset.digest {
			t.Fatalf("asset %s = %#v, want ID %d name %q digest %q", asset.platform, got, asset.id, asset.name, asset.digest)
		}
	}
	if lock.GeoIP.Commit != fixtureCommit {
		t.Fatalf("GeoIP commit = %q, want %q", lock.GeoIP.Commit, fixtureCommit)
	}
	if lock.GeoIP.Country.SHA256 != "c28f761f7671a73f31faf731060c838eba4b0ce12c5fe75242148503ee011518" {
		t.Fatalf("country digest = %q", lock.GeoIP.Country.SHA256)
	}
	if lock.GeoIP.ASN.SHA256 != "c7bd6ecba06b61885f05e59fd15b6c19fed9aad8666c9fa074bd6c222ae592a0" {
		t.Fatalf("ASN digest = %q", lock.GeoIP.ASN.SHA256)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want, err := releaseinputs.Encode(lock)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(want) {
		t.Fatal("resolved lock is not canonical releaseinputs JSON")
	}
	if fixture.requestCount("/repos/MetaCubeX/mihomo/releases/latest") != 1 || fixture.requestCount("/repos/Loyalsoldier/geoip/git/ref/heads/release") != 1 {
		t.Fatal("resolver did not resolve each upstream revision exactly once")
	}
}

func TestSelectResolverAssetIsOrderIndependentAndPrefersReviewedVariants(t *testing.T) {
	tests := []struct {
		name   string
		goos   string
		goarch string
		assets []core.Asset
		want   string
	}{
		{
			name: "arm64 standard over specialized Go toolchains", goos: "darwin", goarch: "arm64",
			assets: []core.Asset{
				{ID: 1, Name: "mihomo-darwin-arm64-go120-v1.19.30.gz"},
				{ID: 2, Name: "mihomo-darwin-arm64-v1.19.30.gz"},
				{ID: 3, Name: "mihomo-darwin-arm64-go122-v1.19.30.gz"},
			},
			want: "mihomo-darwin-arm64-v1.19.30.gz",
		},
		{
			name: "amd64 compatible over other variants", goos: "linux", goarch: "amd64",
			assets: []core.Asset{
				{ID: 4, Name: "mihomo-linux-amd64-v3-v1.19.30.gz"},
				{ID: 5, Name: "mihomo-linux-amd64-compatible-v1.19.30.gz"},
				{ID: 6, Name: "mihomo-linux-amd64-go120-v1.19.30.gz"},
			},
			want: "mihomo-linux-amd64-compatible-v1.19.30.gz",
		},
		{
			name: "fallback is lexical instead of API order", goos: "linux", goarch: "arm64",
			assets: []core.Asset{
				{ID: 7, Name: "mihomo-linux-arm64-go124-v1.19.30.gz"},
				{ID: 8, Name: "mihomo-linux-arm64-go120-v1.19.30.gz"},
			},
			want: "mihomo-linux-arm64-go120-v1.19.30.gz",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, assets := range [][]core.Asset{test.assets, reversedAssets(test.assets)} {
				release := core.Release{TagName: "v1.19.30", Assets: assets}
				got, err := selectResolverAsset(release, test.goos, test.goarch, "stable")
				if err != nil {
					t.Fatalf("selectResolverAsset: %v", err)
				}
				if got.Name != test.want {
					t.Fatalf("selected %q, want %q", got.Name, test.want)
				}
			}
		})
	}
}

func reversedAssets(input []core.Asset) []core.Asset {
	result := make([]core.Asset, len(input))
	for index := range input {
		result[len(input)-1-index] = input[index]
	}
	return result
}

func TestResolveRejectsInvalidGeoIPCommitBeforePayloadDownloads(t *testing.T) {
	fixture := newResolverFixture(t)
	fixture.commitSHA = "release"
	out := filepath.Join(t.TempDir(), "release-inputs.lock.json")
	if err := resolve(context.Background(), fixture.options(out)); err == nil || !strings.Contains(err.Error(), "40 lowercase") {
		t.Fatalf("resolve error = %v, want exact-commit error", err)
	}
	if fixture.requestCount("/Loyalsoldier/geoip/release/GeoLite2-Country.mmdb") != 0 {
		t.Fatal("resolver downloaded GeoIP before validating the immutable commit")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("output exists after invalid commit: %v", err)
	}
}

func TestResolveRejectsGeoIPRefThatDoesNotPointDirectlyToCommit(t *testing.T) {
	fixture := newResolverFixture(t)
	fixture.commitType = "tag"
	out := filepath.Join(t.TempDir(), "release-inputs.lock.json")
	err := resolve(context.Background(), fixture.options(out))
	if err == nil || !strings.Contains(err.Error(), "commit object") {
		t.Fatalf("resolve error = %v, want direct commit object error", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("output exists after tag ref rejection: %v", err)
	}
}

func TestResolvePreservesExistingLockOnDownloadFailure(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*resolverFixture, *options)
	}{
		{
			name: "HTTP status",
			setup: func(fixture *resolverFixture, _ *options) {
				fixture.override = func(response http.ResponseWriter, request *http.Request) bool {
					if strings.Contains(request.URL.Path, "mihomo-linux-amd64-compatible") {
						http.Error(response, "unavailable", http.StatusServiceUnavailable)
						return true
					}
					return false
				}
			},
		},
		{
			name: "declared size mismatch",
			setup: func(fixture *resolverFixture, _ *options) {
				fixture.releaseEdit = func(release *core.Release) { release.Assets[2].Size++ }
			},
		},
		{
			name: "GitHub digest mismatch",
			setup: func(fixture *resolverFixture, _ *options) {
				fixture.releaseEdit = func(release *core.Release) { release.Assets[2].Digest = "sha256:" + strings.Repeat("0", 64) }
			},
		},
		{
			name:  "payload exceeds resolver bound",
			setup: func(_ *resolverFixture, options *options) { options.MaxDownloadBytes = 4 },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newResolverFixture(t)
			dir := t.TempDir()
			out := filepath.Join(dir, "release-inputs.lock.json")
			const old = "reviewed old lock\n"
			if err := os.WriteFile(out, []byte(old), 0o600); err != nil {
				t.Fatal(err)
			}
			options := fixture.options(out)
			test.setup(fixture, &options)
			if err := resolve(context.Background(), options); err == nil {
				t.Fatal("resolve unexpectedly succeeded")
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != old {
				t.Fatalf("old lock changed after failure: %q", got)
			}
			matches, err := filepath.Glob(filepath.Join(dir, ".release-inputs.lock.json-*"))
			if err != nil {
				t.Fatal(err)
			}
			if len(matches) != 0 {
				t.Fatalf("temporary lock files remain after failure: %v", matches)
			}
		})
	}
}

func TestResolveAtomicallyReplacesExistingLockWithoutTemporaryFiles(t *testing.T) {
	fixture := newResolverFixture(t)
	directory := t.TempDir()
	out := filepath.Join(directory, "release-inputs.lock.json")
	if err := os.WriteFile(out, []byte("old reviewed lock\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := resolve(context.Background(), fixture.options(out)); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	lock, err := releaseinputs.Load(out)
	if err != nil {
		t.Fatalf("load replaced lock: %v", err)
	}
	if lock.Mihomo.ReleaseID != 77 || lock.GeoIP.Commit != fixtureCommit {
		t.Fatalf("replaced lock = release %d, commit %q", lock.Mihomo.ReleaseID, lock.GeoIP.Commit)
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".release-inputs.lock.json-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary lock files remain after successful replacement: %v", matches)
	}
}

func TestWriteAtomicReplaceFailurePreservesExistingFileAndCleansTemporary(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "release-inputs.lock.json")
	if err := os.WriteFile(destination, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var source, target string
	err := writeAtomicWithOps(context.Background(), destination, []byte("new\n"), atomicFileOps{
		replace: func(candidate, output string) error {
			source, target = candidate, output
			return errors.New("replace denied")
		},
		syncDirectory: func(string) error {
			t.Fatal("syncDirectory called after failed replacement")
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "replace release input lock") {
		t.Fatalf("writeAtomicWithOps error = %v, want replacement error", err)
	}
	if filepath.Dir(source) != directory || target != destination {
		t.Fatalf("replace = %q -> %q, want same-directory candidate -> destination", source, target)
	}
	got, readErr := os.ReadFile(destination)
	if readErr != nil || string(got) != "old\n" {
		t.Fatalf("destination after replacement failure = %q, %v", got, readErr)
	}
	assertNoLockTemporaries(t, directory)
}

func TestWriteAtomicSyncFailureIsBestEffortAfterSuccessfulReplacement(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "release-inputs.lock.json")
	if err := os.WriteFile(destination, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	syncCalled := false
	err := writeAtomicWithOps(context.Background(), destination, []byte("new\n"), atomicFileOps{
		replace: replaceFile,
		syncDirectory: func(path string) error {
			syncCalled = true
			if path != directory {
				t.Fatalf("sync directory = %q, want %q", path, directory)
			}
			return errors.New("sync denied")
		},
	})
	if err != nil {
		t.Fatalf("writeAtomicWithOps after committed replacement: %v", err)
	}
	if !syncCalled {
		t.Fatal("writeAtomicWithOps did not attempt best-effort parent-directory sync")
	}
	got, readErr := os.ReadFile(destination)
	if readErr != nil || string(got) != "new\n" {
		t.Fatalf("destination after sync failure = %q, %v", got, readErr)
	}
	assertNoLockTemporaries(t, directory)
}

func TestWriteAtomicCancellationBeforeCommitPreservesExistingFile(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "release-inputs.lock.json")
	if err := os.WriteFile(destination, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	replaceCalled := false
	err := writeAtomicWithOps(ctx, destination, []byte("new\n"), atomicFileOps{
		replace: func(string, string) error {
			replaceCalled = true
			return nil
		},
		syncDirectory: func(string) error {
			t.Fatal("syncDirectory called without a committed replacement")
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("writeAtomicWithOps error = %v, want context cancellation", err)
	}
	if replaceCalled {
		t.Fatal("writeAtomicWithOps called replace after cancellation")
	}
	got, readErr := os.ReadFile(destination)
	if readErr != nil || string(got) != "old\n" {
		t.Fatalf("destination after cancellation = %q, %v", got, readErr)
	}
	assertNoLockTemporaries(t, directory)
}

func TestSyncAndCloseJoinsBothErrors(t *testing.T) {
	syncErr := errors.New("sync failed")
	closeErr := errors.New("close failed")
	resource := &syncCloseFixture{syncErr: syncErr, closeErr: closeErr}
	err := syncAndClose(resource)
	if !errors.Is(err, syncErr) || !errors.Is(err, closeErr) {
		t.Fatalf("syncAndClose error = %v, want both sync and close causes", err)
	}
	if resource.syncCalls != 1 || resource.closeCalls != 1 {
		t.Fatalf("calls = sync %d, close %d; want one each", resource.syncCalls, resource.closeCalls)
	}
}

type syncCloseFixture struct {
	syncErr    error
	closeErr   error
	syncCalls  int
	closeCalls int
}

func (fixture *syncCloseFixture) Sync() error {
	fixture.syncCalls++
	return fixture.syncErr
}

func (fixture *syncCloseFixture) Close() error {
	fixture.closeCalls++
	return fixture.closeErr
}

func assertNoLockTemporaries(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, ".release-inputs.lock.json-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary lock files remain: %v", matches)
	}
}

func TestNetworkErrorsRedactURLsAndPreserveContextCause(t *testing.T) {
	operations := []struct {
		name string
		run  func(*http.Client) error
	}{
		{
			name: "resolve GeoIP commit",
			run: func(client *http.Client) error {
				_, err := resolveCommit(context.Background(), client, withDefaults(options{}))
				return err
			},
		},
		{
			name: "download payload",
			run: func(client *http.Client) error {
				_, err := downloadDigest(context.Background(), client, "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/asset.gz", 0, 1024, "", mihomoPayload, "")
				return err
			},
		},
	}
	causes := []struct {
		name string
		err  error
	}{
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
	}
	for _, operation := range operations {
		for _, cause := range causes {
			t.Run(operation.name+"/"+cause.name, func(t *testing.T) {
				const secretURL = "https://signed.example/download?token=secret-value"
				client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, &url.Error{Op: "Get", URL: secretURL, Err: cause.err}
				})}
				err := operation.run(client)
				if err == nil {
					t.Fatal("operation unexpectedly succeeded")
				}
				if !errors.Is(err, cause.err) {
					t.Fatalf("errors.Is(%v, %v) = false", err, cause.err)
				}
				for _, secret := range []string{"secret-value", secretURL, "signed.example", "token="} {
					if strings.Contains(err.Error(), secret) {
						t.Fatalf("network error leaked %q: %v", secret, err)
					}
				}
			})
		}
	}
}

func TestResolveRejectsUnapprovedAssetURLBeforeDownload(t *testing.T) {
	fixture := newResolverFixture(t)
	fixture.releaseEdit = func(release *core.Release) {
		release.Assets[0].URL = "https://evil.example/secret?token=do-not-log"
	}
	out := filepath.Join(t.TempDir(), "release-inputs.lock.json")
	err := resolve(context.Background(), fixture.options(out))
	if err == nil || !strings.Contains(err.Error(), "URL") {
		t.Fatalf("resolve error = %v, want safe URL error", err)
	}
	if strings.Contains(err.Error(), "do-not-log") || strings.Contains(err.Error(), "evil.example") {
		t.Fatalf("resolver leaked rejected URL: %v", err)
	}
	for _, asset := range fixture.assets {
		if fixture.requestCount("/MetaCubeX/mihomo/releases/download/v1.19.30/"+asset.name) != 0 {
			t.Fatal("resolver downloaded payload before validating locked URL metadata")
		}
	}
}

func TestResolveRejectsCrossOriginHTTPSRedirectBeforePayloadRequest(t *testing.T) {
	var evilHits atomic.Int32
	evil := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		evilHits.Add(1)
		_, _ = response.Write([]byte("evil-payload"))
	}))
	defer evil.Close()
	fixture := newResolverFixture(t)
	fixture.override = func(response http.ResponseWriter, request *http.Request) bool {
		if strings.Contains(request.URL.Path, "mihomo-darwin-amd64-compatible") {
			http.Redirect(response, request, evil.URL+"/signed?token=secret-value", http.StatusFound)
			return true
		}
		return false
	}
	directory := t.TempDir()
	out := filepath.Join(directory, "release-inputs.lock.json")
	if err := os.WriteFile(out, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolverOptions := fixture.options(out)
	resolverOptions.HTTPClient = evil.Client()
	err := resolve(context.Background(), resolverOptions)
	if err == nil {
		t.Fatal("resolve accepted cross-origin HTTPS payload redirect")
	}
	for _, secret := range []string{"secret-value", evil.URL, "token="} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("redirect error leaked %q: %v", secret, err)
		}
	}
	if evilHits.Load() != 0 {
		t.Fatalf("cross-origin redirect target received %d requests, want 0", evilHits.Load())
	}
	got, readErr := os.ReadFile(out)
	if readErr != nil || string(got) != "old\n" {
		t.Fatalf("lock after rejected redirect = %q, %v", got, readErr)
	}
	assertNoLockTemporaries(t, directory)
}

func TestDownloadDigestRejectsUnapprovedFinalURLBeforeReadingBody(t *testing.T) {
	body := &readTrackingBody{reader: strings.NewReader("evil-payload")}
	finalURL, err := url.Parse("https://evil.example/signed?token=secret-value")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       body,
			Request:    &http.Request{URL: finalURL},
		}, nil
	})}
	_, err = downloadDigest(
		context.Background(),
		client,
		"https://raw.githubusercontent.com/Loyalsoldier/geoip/0123456789abcdef0123456789abcdef01234567/GeoLite2-Country.mmdb",
		0,
		1024,
		"",
		geoIPPayload,
		"",
	)
	if err == nil {
		t.Fatal("downloadDigest accepted unapproved final URL")
	}
	if body.readCalls != 0 {
		t.Fatalf("unapproved response body read %d times, want 0", body.readCalls)
	}
	for _, secret := range []string{"evil.example", "secret-value", "token="} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("final URL error leaked %q: %v", secret, err)
		}
	}
}

func TestPayloadURLPolicyProductionHostContract(t *testing.T) {
	valid := []struct {
		name   string
		source payloadSource
		rawURL string
	}{
		{name: "mihomo release page", source: mihomoPayload, rawURL: "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/core.gz"},
		{name: "mihomo release assets", source: mihomoPayload, rawURL: "https://release-assets.githubusercontent.com/github-production-release-asset/123/core.gz?sig=signed"},
		{name: "mihomo objects", source: mihomoPayload, rawURL: "https://objects.githubusercontent.com/github-production-release-asset/core.gz?sig=signed"},
		{name: "GeoIP raw content", source: geoIPPayload, rawURL: "https://raw.githubusercontent.com/Loyalsoldier/geoip/0123456789abcdef0123456789abcdef01234567/GeoLite2-Country.mmdb"},
	}
	for _, test := range valid {
		t.Run("accept/"+test.name, func(t *testing.T) {
			policy, err := newPayloadURLPolicy(test.source, "")
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := url.Parse(test.rawURL)
			if err != nil {
				t.Fatal(err)
			}
			if err := policy.validate(candidate); err != nil {
				t.Fatalf("validate(%q): %v", test.rawURL, err)
			}
		})
	}

	invalid := []struct {
		name   string
		source payloadSource
		rawURL string
	}{
		{name: "mihomo cannot use GeoIP host", source: mihomoPayload, rawURL: "https://raw.githubusercontent.com/MetaCubeX/mihomo/core.gz"},
		{name: "GeoIP cannot use GitHub release host", source: geoIPPayload, rawURL: "https://github.com/Loyalsoldier/geoip/file.mmdb"},
		{name: "evil host", source: mihomoPayload, rawURL: "https://evil.example/payload?token=secret-value"},
		{name: "subdomain spoof", source: mihomoPayload, rawURL: "https://github.com.evil.example/payload"},
		{name: "port", source: mihomoPayload, rawURL: "https://github.com:443/payload"},
		{name: "HTTP", source: mihomoPayload, rawURL: "http://github.com/payload"},
		{name: "userinfo", source: mihomoPayload, rawURL: "https://secret-user@github.com/payload"},
		{name: "GeoIP subdomain spoof", source: geoIPPayload, rawURL: "https://raw.githubusercontent.com.evil.example/file.mmdb"},
	}
	for _, test := range invalid {
		t.Run("reject/"+test.name, func(t *testing.T) {
			policy, err := newPayloadURLPolicy(test.source, "")
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := url.Parse(test.rawURL)
			if err != nil {
				t.Fatal(err)
			}
			err = policy.validate(candidate)
			if err == nil {
				t.Fatalf("validate accepted %q", test.rawURL)
			}
			for _, sensitive := range []string{test.rawURL, "secret-value", "secret-user", "evil.example"} {
				if strings.Contains(err.Error(), sensitive) {
					t.Fatalf("policy error leaked %q: %v", sensitive, err)
				}
			}
		})
	}
}

type readTrackingBody struct {
	reader    *strings.Reader
	readCalls int
}

func (body *readTrackingBody) Read(destination []byte) (int, error) {
	body.readCalls++
	return body.reader.Read(destination)
}

func (*readTrackingBody) Close() error { return nil }

func TestResolvePropagatesCanceledDownloadContextAndPreservesOutput(t *testing.T) {
	fixture := newResolverFixture(t)
	fixture.blockPayload = "darwin/amd64"
	dir := t.TempDir()
	out := filepath.Join(dir, "release-inputs.lock.json")
	if err := os.WriteFile(out, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- resolve(ctx, fixture.options(out))
	}()
	<-fixture.payloadStart
	cancel()
	err := <-result
	if err == nil {
		t.Fatal("resolve with canceled context unexpectedly succeeded")
	}
	got, readErr := os.ReadFile(out)
	if readErr != nil || string(got) != "old\n" {
		t.Fatalf("old lock after cancellation = %q, %v", got, readErr)
	}
}

func TestParseOptionsImplementsDocumentedCLIContract(t *testing.T) {
	got, err := parseOptions([]string{"--channel", "stable", "--out", "scripts/release-inputs.lock.json"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if got.Channel != "stable" || got.Out != "scripts/release-inputs.lock.json" {
		t.Fatalf("options = %#v", got)
	}
	if _, err := parseOptions([]string{"--channel", "nightly", "--out", "lock.json"}); err == nil {
		t.Fatal("parseOptions accepted unsupported channel")
	}
}

func TestParseOptionsRejectsGitHubTokenFlag(t *testing.T) {
	_, err := parseOptions([]string{"--github-token", "secret-value"})
	if err == nil {
		t.Fatal("parseOptions accepted command-line GitHub token")
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("parseOptions error leaked token: %v", err)
	}
}

func TestRunCLIHelpPrintsUsageAndExitsSuccessfully(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI(context.Background(), []string{"--help"}, &stdout, &stderr, func(string) string {
		return "secret-from-environment"
	})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage of resolve-release-inputs") || !strings.Contains(stdout.String(), "-channel") {
		t.Fatalf("help output missing usage: %q", stdout.String())
	}
	for _, forbidden := range []string{"github-token", "GITHUB_TOKEN", "secret-from-environment"} {
		if strings.Contains(stdout.String(), forbidden) || strings.Contains(stderr.String(), forbidden) {
			t.Fatalf("help output exposed %q: stdout=%q stderr=%q", forbidden, stdout.String(), stderr.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunCLIWiresEnvironmentTokenOnlyToAPIRequests(t *testing.T) {
	fixture := newResolverFixture(t)
	serverURL, err := url.Parse(fixture.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var headersMu sync.Mutex
	headers := make(map[string][]string)
	mapper := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		headersMu.Lock()
		headers[request.URL.Path] = append(headers[request.URL.Path], request.Header.Get("Authorization"))
		headersMu.Unlock()
		forwarded := request.Clone(request.Context())
		forwarded.URL.Scheme = serverURL.Scheme
		forwarded.URL.Host = serverURL.Host
		response, err := fixture.server.Client().Transport.RoundTrip(forwarded)
		if response != nil {
			// Preserve the original HTTPS URL for resolver redirect validation.
			response.Request = request
		}
		return response, err
	})
	execute := func(ctx context.Context, options options) error {
		options.HTTPClient = &http.Client{
			Transport:     bearerTransport{base: mapper, token: options.GitHubToken, allowedHost: "api.github.com"},
			CheckRedirect: redirectPolicy,
		}
		options.APIBase = defaultAPIBase
		return resolve(ctx, options)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	out := filepath.Join(t.TempDir(), "release-inputs.lock.json")
	exitCode := runCLIWithResolver(
		context.Background(),
		[]string{"--channel", "stable", "--out", out},
		&stdout,
		&stderr,
		func(name string) string {
			if name == "GITHUB_TOKEN" {
				return "environment-secret"
			}
			return ""
		},
		execute,
	)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}

	headersMu.Lock()
	defer headersMu.Unlock()
	apiRequests := 0
	payloadRequests := 0
	for path, values := range headers {
		for _, authorization := range values {
			if strings.HasPrefix(path, "/repos/") {
				apiRequests++
				if authorization != "Bearer environment-secret" {
					t.Fatalf("API %s authorization = %q", path, authorization)
				}
			} else {
				payloadRequests++
				if authorization != "" {
					t.Fatalf("payload %s received authorization %q", path, authorization)
				}
			}
		}
	}
	if apiRequests != 2 || payloadRequests != 8 {
		t.Fatalf("request counts = %d API, %d payload; want 2 and 8", apiRequests, payloadRequests)
	}
	if strings.Contains(stdout.String(), "environment-secret") || strings.Contains(stderr.String(), "environment-secret") {
		t.Fatal("CLI output leaked environment token")
	}
}

func TestRedirectPolicyRejectsHTTPSDowngrade(t *testing.T) {
	previous := &http.Request{URL: &url.URL{Scheme: "https", Host: "github.com"}}
	if err := redirectPolicy(&http.Request{URL: &url.URL{Scheme: "http", Host: "example.com"}}, []*http.Request{previous}); err == nil {
		t.Fatal("redirect policy accepted HTTPS-to-HTTP downgrade")
	}
	if err := redirectPolicy(&http.Request{URL: &url.URL{Scheme: "https", Host: "release-assets.githubusercontent.com"}}, []*http.Request{previous}); err != nil {
		t.Fatalf("redirect policy rejected HTTPS redirect: %v", err)
	}
}

func TestBearerTransportDoesNotSendTokenToPayloadHosts(t *testing.T) {
	var headers []string
	base := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		headers = append(headers, request.Header.Get("Authorization"))
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: request}, nil
	})
	transport := bearerTransport{base: base, token: "secret-token", allowedHost: "api.github.com"}
	for _, rawURL := range []string{
		"https://api.github.com/repos/MetaCubeX/mihomo/releases/latest",
		"https://raw.githubusercontent.com/Loyalsoldier/geoip/commit/GeoLite2-Country.mmdb",
	} {
		request, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := transport.RoundTrip(request); err != nil {
			t.Fatal(err)
		}
	}
	if len(headers) != 2 || headers[0] != "Bearer secret-token" || headers[1] != "" {
		t.Fatalf("authorization headers = %q, want token only for API host", headers)
	}
}

func TestNewHTTPClientRejectsUnsafeAPIBase(t *testing.T) {
	tests := []string{
		"://malformed-token",
		"/relative/api",
		"http://api.github.com",
		"https:///missing-host",
		"https://user-secret@api.github.com",
		"https://api.github.com?token=secret-value",
		"https://api.github.com#secret-fragment",
		"https://api.github.com#",
	}
	for _, rawBase := range tests {
		t.Run(strings.ReplaceAll(rawBase, "/", "_"), func(t *testing.T) {
			client, err := newHTTPClient("environment-secret", rawBase)
			if err == nil || client != nil {
				t.Fatalf("newHTTPClient(%q) = %#v, %v; want rejection", rawBase, client, err)
			}
			for _, secret := range []string{rawBase, "malformed-token", "user-secret", "secret-value", "secret-fragment", "environment-secret"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("API base error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestResolveRejectsUnsafeAPIBaseWithoutMutatingLock(t *testing.T) {
	for _, rawBase := range []string{"://malformed-token", "/relative/api", "http://api.github.com", "https://user-secret@api.github.com", "https://api.github.com?token=secret-value"} {
		t.Run(strings.ReplaceAll(rawBase, "/", "_"), func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "release-inputs.lock.json")
			if err := os.WriteFile(destination, []byte("old\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := resolve(context.Background(), options{Out: destination, APIBase: rawBase, GitHubToken: "environment-secret"})
			if err == nil {
				t.Fatalf("resolve accepted unsafe API base %q", rawBase)
			}
			for _, secret := range []string{rawBase, "malformed-token", "user-secret", "secret-value", "environment-secret"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("resolve API base error leaked %q: %v", secret, err)
				}
			}
			got, readErr := os.ReadFile(destination)
			if readErr != nil || string(got) != "old\n" {
				t.Fatalf("lock after unsafe API base = %q, %v", got, readErr)
			}
			assertNoLockTemporaries(t, filepath.Dir(destination))
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
