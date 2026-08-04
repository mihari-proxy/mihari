package subscription

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDownloaderConditionalRequestAndNotModified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("If-None-Match") != `"v1"` || request.Header.Get("If-Modified-Since") != "yesterday" {
			t.Errorf("conditional headers missing: %#v", request.Header)
		}
		if request.Header.Get("User-Agent") != subscriptionUserAgent {
			t.Errorf("user agent=%q", request.Header.Get("User-Agent"))
		}
		writer.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	result, err := NewDownloader(server.Client()).Fetch(context.Background(), FetchRequest{URL: server.URL, ETag: `"v1"`, LastModified: "yesterday"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.NotModified || len(result.Content) != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestDownloader_UserAgentLooksLikeClashClient(t *testing.T) {
	var userAgent string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		userAgent = request.Header.Get("User-Agent")
		_, _ = writer.Write([]byte("proxies: []\n"))
	}))
	defer server.Close()
	if _, err := NewDownloader(server.Client()).Fetch(context.Background(), FetchRequest{URL: server.URL}); err != nil {
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
	downloader := NewDownloader(server.Client())
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
	if _, err := NewDownloader(server.Client()).Fetch(context.Background(), FetchRequest{URL: server.URL}); err == nil {
		t.Fatal("expected status failure")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewDownloader(server.Client()).Fetch(ctx, FetchRequest{URL: server.URL})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
