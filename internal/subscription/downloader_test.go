package subscription

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestDownloaderConditionalRequestAndNotModified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("If-None-Match") != `"v1"` || request.Header.Get("If-Modified-Since") != "yesterday" {
			t.Errorf("conditional headers missing: %#v", request.Header)
		}
		if request.Header.Get("User-Agent") != subscriptionUserAgent {
			t.Errorf("user agent=%q", request.Header.Get("User-Agent"))
		}
		writer.Header().Set("Subscription-Userinfo", "upload=1; download=2; total=3")
		writer.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	result, err := NewDownloader(DownloaderOptions{Client: server.Client()}).Fetch(context.Background(), FetchRequest{URL: server.URL, ETag: `"v1"`, LastModified: "yesterday"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.NotModified || len(result.Content) != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Userinfo != "upload=1; download=2; total=3" {
		t.Fatalf("userinfo=%q", result.Userinfo)
	}
}

func TestDownloader_UserAgentLooksLikeClashClient(t *testing.T) {
	var userAgent string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		userAgent = request.Header.Get("User-Agent")
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	defer server.Close()
	if _, err := NewDownloader(DownloaderOptions{Client: server.Client()}).Fetch(context.Background(), FetchRequest{URL: server.URL}); err != nil {
		t.Fatal(err)
	}
	if userAgent != subscriptionUserAgent {
		t.Fatalf("user agent=%q want %q", userAgent, subscriptionUserAgent)
	}
	if !strings.Contains(strings.ToLower(userAgent), "clash") {
		t.Fatalf("user agent should look Clash-like: %q", userAgent)
	}
}

func TestDownloaderBoundsBodyAndRedactsURLFromErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(strings.Repeat("x", 33)))
	}))
	defer server.Close()
	downloader := NewDownloader(DownloaderOptions{Client: server.Client()})
	downloader.MaxBytes = 32
	_, err := downloader.Fetch(context.Background(), FetchRequest{URL: server.URL + "?token=secret"})
	if err == nil {
		t.Fatal("expected size failure")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error leaked URL: %v", err)
	}
}

func TestDownloaderMapsStatusAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	if _, err := NewDownloader(DownloaderOptions{Client: server.Client()}).Fetch(context.Background(), FetchRequest{URL: server.URL}); err == nil {
		t.Fatal("expected status failure")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewDownloader(DownloaderOptions{Client: server.Client()}).Fetch(ctx, FetchRequest{URL: server.URL})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

// proxyCountingServers builds a direct and a proxy server that each count how
// many fetches reach them. The proxy server is a deliberately naive forward
// proxy: it answers requests itself rather than forwarding, which is enough to
// observe which client the downloader selected.
func proxyCountingServers(t *testing.T, body []byte) (direct, proxy *httptest.Server, directHits, proxyHits *int32) {
	t.Helper()
	directHits = new(int32)
	proxyHits = new(int32)
	direct = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(directHits, 1)
		_, _ = writer.Write(body)
	}))
	proxy = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(proxyHits, 1)
		_, _ = writer.Write(body)
	}))
	t.Cleanup(direct.Close)
	t.Cleanup(proxy.Close)
	return direct, proxy, directHits, proxyHits
}

func mustProxyURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	return parsed
}

func TestDownloaderSelectsClientByMode(t *testing.T) {
	body := []byte("proxies: []\nrules: [MATCH,DIRECT]\n")
	directServer, proxyServer, directHits, proxyHits := proxyCountingServers(t, body)
	downloader := NewDownloader(DownloaderOptions{Client: directServer.Client(), ProxyURL: mustProxyURL(t, proxyServer.URL)})

	if _, err := downloader.Fetch(context.Background(), FetchRequest{URL: directServer.URL, Mode: ProxyModeDirect}); err != nil {
		t.Fatalf("direct fetch: %v", err)
	}
	if got := atomic.LoadInt32(directHits); got != 1 {
		t.Fatalf("direct mode should hit direct server only: direct=%d proxy=%d", got, *proxyHits)
	}

	if _, err := downloader.Fetch(context.Background(), FetchRequest{URL: directServer.URL, Mode: ProxyModeProxy}); err != nil {
		t.Fatalf("proxy fetch: %v", err)
	}
	if got := atomic.LoadInt32(proxyHits); got != 1 {
		t.Fatalf("proxy mode should hit proxy server: direct=%d proxy=%d", *directHits, got)
	}
}

func TestDownloaderAutoPrefersProxyWithoutFallback(t *testing.T) {
	body := []byte("proxies: []\nrules: [MATCH,DIRECT]\n")
	directServer, proxyServer, directHits, proxyHits := proxyCountingServers(t, body)
	downloader := NewDownloader(DownloaderOptions{Client: directServer.Client(), ProxyURL: mustProxyURL(t, proxyServer.URL)})

	result, err := downloader.Fetch(context.Background(), FetchRequest{URL: directServer.URL, Mode: ProxyModeAuto})
	if err != nil {
		t.Fatalf("auto fetch: %v", err)
	}
	if result.FellBack {
		t.Fatal("proxy success should not set FellBack")
	}
	if got := atomic.LoadInt32(proxyHits); got != 1 {
		t.Fatalf("auto should prefer proxy: proxy=%d", got)
	}
	if got := atomic.LoadInt32(directHits); got != 0 {
		t.Fatalf("auto should not touch direct when proxy succeeds: direct=%d", got)
	}
}

func TestDownloaderAutoFallsBackOnConnectionRefused(t *testing.T) {
	var directHits int32
	directServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&directHits, 1)
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	t.Cleanup(directServer.Close)
	// A closed server leaves the port unreachable → connection refused.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead.Close()

	downloader := NewDownloader(DownloaderOptions{Client: directServer.Client(), ProxyURL: mustProxyURL(t, dead.URL)})
	result, err := downloader.Fetch(context.Background(), FetchRequest{URL: directServer.URL, Mode: ProxyModeAuto})
	if err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}
	if !result.FellBack {
		t.Fatal("expected FellBack=true after proxy connection refused")
	}
	if got := atomic.LoadInt32(&directHits); got != 1 {
		t.Fatalf("expected direct fallback hit: %d", got)
	}
}

func TestDownloaderAutoFallsBackOnTimeout(t *testing.T) {
	var directHits int32
	directServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&directHits, 1)
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	t.Cleanup(directServer.Close)
	// Accepts the connection but never responds; returns when the client gives up.
	hanging := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	t.Cleanup(hanging.Close)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(mustProxyURL(t, hanging.URL))
	proxyClient := &http.Client{Transport: transport, Timeout: 100 * time.Millisecond}
	downloader := &Downloader{direct: directServer.Client(), proxy: proxyClient, MaxBytes: defaultMaxSubscriptionBytes}

	result, err := downloader.Fetch(context.Background(), FetchRequest{URL: directServer.URL, Mode: ProxyModeAuto})
	if err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}
	if !result.FellBack {
		t.Fatal("expected FellBack=true after proxy timeout")
	}
	if got := atomic.LoadInt32(&directHits); got != 1 {
		t.Fatalf("expected direct fallback hit: %d", got)
	}
}

func TestDownloaderAutoDoesNotFallBackOnHTTPStatusError(t *testing.T) {
	var directHits int32
	proxyServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(proxyServer.Close)
	directServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&directHits, 1)
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	t.Cleanup(directServer.Close)

	downloader := NewDownloader(DownloaderOptions{Client: directServer.Client(), ProxyURL: mustProxyURL(t, proxyServer.URL)})
	if _, err := downloader.Fetch(context.Background(), FetchRequest{URL: directServer.URL, Mode: ProxyModeAuto}); err == nil {
		t.Fatal("expected proxy 404 to fail without fallback")
	}
	if got := atomic.LoadInt32(&directHits); got != 0 {
		t.Fatalf("direct should not be tried after an HTTP status error: %d", got)
	}
}

func TestDownloaderProxyModeDoesNotFallBack(t *testing.T) {
	directServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	t.Cleanup(directServer.Close)
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead.Close()

	downloader := NewDownloader(DownloaderOptions{Client: directServer.Client(), ProxyURL: mustProxyURL(t, dead.URL)})
	_, err := downloader.Fetch(context.Background(), FetchRequest{URL: directServer.URL, Mode: ProxyModeProxy})
	if err == nil {
		t.Fatal("expected proxy-only failure")
	}
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeNetworkFailure {
		t.Fatalf("expected CodeNetworkFailure for caller, got %v", err)
	}
}

func TestDownloaderProxyModeWithoutProxyConfigIsInvalidState(t *testing.T) {
	directServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	t.Cleanup(directServer.Close)
	downloader := NewDownloader(DownloaderOptions{Client: directServer.Client()}) // no ProxyURL

	for _, mode := range []string{ProxyModeProxy, ProxyModeAuto} {
		_, err := downloader.Fetch(context.Background(), FetchRequest{URL: directServer.URL, Mode: mode})
		var apiError protocol.APIError
		if !errors.As(err, &apiError) || apiError.Code != protocol.CodeInvalidState {
			t.Fatalf("mode=%q expected CodeInvalidState, got %v", mode, err)
		}
	}
}

// TestDownloaderAutoDoesNotFallBackOnContextCancellation locks the contract that a
// cancelled context short-circuits the auto fallback loop: the proxy attempt must
// surface context.Canceled, never a wasted direct retry.
func TestDownloaderAutoDoesNotFallBackOnContextCancellation(t *testing.T) {
	var directHits int32
	directServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&directHits, 1)
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	t.Cleanup(directServer.Close)
	// Hanging proxy: accepts the connection but blocks until the request ends.
	hanging := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	t.Cleanup(hanging.Close)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(mustProxyURL(t, hanging.URL))
	proxyClient := &http.Client{Transport: transport}
	downloader := &Downloader{direct: directServer.Client(), proxy: proxyClient, MaxBytes: defaultMaxSubscriptionBytes}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := downloader.Fetch(ctx, FetchRequest{URL: directServer.URL, Mode: ProxyModeAuto})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if got := atomic.LoadInt32(&directHits); got != 0 {
		t.Fatalf("cancel should short-circuit before direct fallback: directHits=%d", got)
	}
}
