package update

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type officialTransport func(*http.Request) (*http.Response, error)

func (f officialTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestOfficialRelease_IndependentFixedTagDigest(t *testing.T) {
	calls := 0
	want := fixtureSHA256Hex([]byte("official binary"))
	source := OfficialReleaseSource{Client: &http.Client{Transport: officialTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://github.com/mihari-proxy/mihari/releases/download/v1.2.3/SHA256SUMS.txt" {
			t.Fatalf("nonofficial or mutable authority: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(want + "  mihari-linux-amd64\n")), Header: make(http.Header)}, nil
	})}}
	got, err := source.Checksum(context.Background(), "v1.2.3", "mihari-linux-amd64")
	if err != nil || got != want || calls != 1 {
		t.Fatalf("independent official evidence missing: digest=%q calls=%d err=%v", got, calls, err)
	}
}
