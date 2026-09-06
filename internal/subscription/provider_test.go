package subscription

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

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
