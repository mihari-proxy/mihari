package subscription

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type providerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f providerRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestProviderDownloader_AutoFallsBackAfterAttemptTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		proxyCalls, directCalls := 0, 0
		d := &Downloader{
			proxy: &http.Client{Transport: providerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				proxyCalls++
				<-r.Context().Done()
				return nil, r.Context().Err()
			})},
			direct: &http.Client{Transport: providerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				directCalls++
				// synctest advances this clock without a wall-clock delay.
				time.Sleep(time.Second)
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("provider")), Header: make(http.Header)}, nil
			})},
		}
		start := time.Now()
		got, err := d.Download(context.Background(), ProviderSpec{URL: "https://provider.test/source"}, ProxyModeAuto)
		if err != nil || string(got) != "provider" || proxyCalls != 1 || directCalls != 1 {
			t.Fatalf("proxy timeout prevented direct fallback: content=%q proxy=%d direct=%d err=%v", got, proxyCalls, directCalls, err)
		}
		if elapsed := time.Since(start); elapsed != 31*time.Second {
			t.Fatalf("attempt deadline was not enforced: %v", elapsed)
		}
	})
}

func TestProviderDownloader_CallerDeadlineStopsAutoFallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		directCalls := 0
		d := &Downloader{
			proxy: &http.Client{Transport: providerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				<-r.Context().Done()
				return nil, r.Context().Err()
			})},
			direct: &http.Client{Transport: providerRoundTripFunc(func(*http.Request) (*http.Response, error) {
				directCalls++
				return nil, errors.New("unexpected direct attempt")
			})},
		}
		got, err := d.Download(ctx, ProviderSpec{URL: "https://provider.test/source"}, ProxyModeAuto)
		if !errors.Is(err, context.DeadlineExceeded) || len(got) != 0 || directCalls != 0 {
			t.Fatalf("caller cancellation ignored: direct=%d err=%v", directCalls, err)
		}
	})
}

func TestProviderDownloader_ConsumesTypedHeadersAndSourceLimit(t *testing.T) {
	var headers []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Values("X-Provider")
		_, _ = w.Write([]byte("12345"))
	}))
	defer server.Close()
	d := NewDownloader(DownloaderOptions{})
	spec := ProviderSpec{URL: server.URL, Header: map[string][]string{"X-Provider": {"one", "two"}}, MaxBytes: 4}
	got, err := d.Download(context.Background(), spec, ProxyModeDirect)
	if err == nil || len(got) != 0 {
		t.Fatalf("oversize source accepted: %q, %v", got, err)
	}
	if !reflect.DeepEqual(headers, []string{"one", "two"}) {
		t.Fatalf("typed header values lost: %q", headers)
	}
}

func TestProviderDownloader_AllowsFiveRedirectsAndRejectsSix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var hops int
		_, _ = fmt.Sscanf(r.URL.Path, "/%d", &hops)
		if hops > 0 {
			http.Redirect(w, r, fmt.Sprintf("/%d", hops-1), http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	d := NewDownloader(DownloaderOptions{})
	got, err := d.Download(context.Background(), ProviderSpec{URL: server.URL + "/5"}, ProxyModeDirect)
	if err != nil || string(got) != "ok" {
		t.Fatalf("five redirects rejected: %q, %v", got, err)
	}
	got, err = d.Download(context.Background(), ProviderSpec{URL: server.URL + "/6"}, ProxyModeDirect)
	if err == nil || len(got) != 0 {
		t.Fatalf("six redirects accepted: %q, %v", got, err)
	}
}
