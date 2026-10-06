package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const (
	channelIndexStableURL = "https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt"
	channelIndexDevURL    = "https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari-dev/index.txt"
	channelIndexSum       = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	channelIndexOtherSum  = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
)

func TestChannelIndex_URL(t *testing.T) {
	mainURL, err := channelIndexURL(InstallChannelMain)
	if err != nil || mainURL != channelIndexStableURL {
		t.Fatalf("main url=%q err=%v", mainURL, err)
	}
	devURL, err := channelIndexURL(InstallChannelDev)
	if err != nil || devURL != channelIndexDevURL {
		t.Fatalf("dev url=%q err=%v", devURL, err)
	}
	for _, channel := range []string{"", "stable", "MAIN", "Dev"} {
		if _, err := channelIndexURL(channel); err == nil {
			t.Fatalf("channel %q accepted", channel)
		}
	}
}

func TestChannelIndex_ParsesTagAndSum(t *testing.T) {
	if len(channelIndexSum) != 64 || len(channelIndexOtherSum) != 64 {
		t.Fatalf("fixture sums len=%d %d", len(channelIndexSum), len(channelIndexOtherSum))
	}
	text := strings.Join([]string{
		"",
		"# comment",
		"// note",
		"  # indented",
		"  // indented",
		"latest v1.2.3",
		"darwin-arm64   https://example.invalid/b   " + channelIndexOtherSum,
		"  linux-amd64   https://example.invalid/a   " + channelIndexSum,
		"",
	}, "\n")
	latest, sum, err := parseChannelIndex(text, InstallChannelMain, "linux", "amd64")
	if err != nil || latest != "v1.2.3" || sum != channelIndexSum {
		t.Fatalf("latest=%q sum=%q err=%v", latest, sum, err)
	}

	devText := "latest v1.2.3-dev.4\nlinux-amd64 https://example.invalid/a " + channelIndexSum + "\n"
	latest, sum, err = parseChannelIndex(devText, InstallChannelDev, "linux", "amd64")
	if err != nil || latest != "v1.2.3-dev.4" || sum != channelIndexSum {
		t.Fatalf("dev latest=%q sum=%q err=%v", latest, sum, err)
	}

	for _, tag := range []string{"v0.0.0", "v0.10.0", "v10.0.0"} {
		latest, sum, err = parseChannelIndex(channelIndexBody(tag, channelIndexSum), InstallChannelMain, "linux", "amd64")
		if err != nil || latest != tag || sum != channelIndexSum {
			t.Fatalf("tag %s latest=%q sum=%q err=%v", tag, latest, sum, err)
		}
	}
	for _, tag := range []string{"v0.0.0-dev.0", "v1.2.3-dev.10"} {
		latest, sum, err = parseChannelIndex(channelIndexBody(tag, channelIndexSum), InstallChannelDev, "linux", "amd64")
		if err != nil || latest != tag || sum != channelIndexSum {
			t.Fatalf("tag %s latest=%q sum=%q err=%v", tag, latest, sum, err)
		}
	}
}

func TestChannelIndex_Rejects(t *testing.T) {
	valid := channelIndexBody("v1.2.3", channelIndexSum)
	cases := []struct {
		name, channel, goos, goarch, text string
	}{
		{name: "dev tag on main", channel: InstallChannelMain, goos: "linux", goarch: "amd64", text: channelIndexBody("v1.2.3-dev.4", channelIndexSum)},
		{name: "stable tag on dev", channel: InstallChannelDev, goos: "linux", goarch: "amd64", text: valid},
		{name: "duplicate latest", channel: InstallChannelMain, goos: "linux", goarch: "amd64", text: "latest v1.2.3\nlatest v1.2.4\nlinux-amd64 https://example.invalid/a " + channelIndexSum + "\n"},
		{name: "duplicate platform", channel: InstallChannelMain, goos: "linux", goarch: "amd64", text: valid + "linux-amd64 https://example.invalid/a " + channelIndexSum + "\n"},
		{name: "duplicate other platform", channel: InstallChannelMain, goos: "linux", goarch: "amd64", text: valid + "darwin-arm64 https://example.invalid/b " + channelIndexOtherSum + "\ndarwin-arm64 https://example.invalid/b " + channelIndexOtherSum + "\n"},
		{name: "uppercase sha256", channel: InstallChannelMain, goos: "linux", goarch: "amd64", text: channelIndexBody("v1.2.3", strings.ToUpper(channelIndexSum))},
		{name: "short sha256", channel: InstallChannelMain, goos: "linux", goarch: "amd64", text: channelIndexBody("v1.2.3", channelIndexSum[:63])},
		{name: "non-hex sha256", channel: InstallChannelMain, goos: "linux", goarch: "amd64", text: channelIndexBody("v1.2.3", strings.Repeat("g", 64))},
		{name: "other platform bad sha256", channel: InstallChannelMain, goos: "linux", goarch: "amd64", text: valid + "darwin-arm64 https://example.invalid/b " + channelIndexSum[:10] + "\n"},
		{name: "missing latest", channel: InstallChannelMain, goos: "linux", goarch: "amd64", text: "linux-amd64 https://example.invalid/a " + channelIndexSum + "\n"},
		{name: "missing platform", channel: InstallChannelMain, goos: "linux", goarch: "amd64", text: "latest v1.2.3\n"},
		{name: "leading zero", channel: InstallChannelMain, goos: "linux", goarch: "amd64", text: channelIndexBody("v1.02.3", channelIndexSum)},
		{name: "dev leading zero", channel: InstallChannelDev, goos: "linux", goarch: "amd64", text: channelIndexBody("v1.2.3-dev.04", channelIndexSum)},
		{name: "unknown channel", channel: "stable", goos: "linux", goarch: "amd64", text: valid},
		{name: "oversize", channel: InstallChannelMain, goos: "linux", goarch: "amd64", text: valid + strings.Repeat("\n", 65537-len(valid))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			latest, sum, err := parseChannelIndex(tc.text, tc.channel, tc.goos, tc.goarch)
			if err == nil {
				t.Fatalf("expected error, latest=%q sum=%q", latest, sum)
			}
		})
	}
}

func TestChannelIndex_SizeBoundary(t *testing.T) {
	body := channelIndexBody("v1.2.3", channelIndexSum)
	if len(body) >= 65536 {
		t.Fatalf("fixture length %d", len(body))
	}
	exact := body + "#" + strings.Repeat(".", 65536-len(body)-1)
	if len(exact) != 65536 {
		t.Fatalf("boundary length %d", len(exact))
	}
	latest, sum, err := parseChannelIndex(exact, InstallChannelMain, "linux", "amd64")
	if err != nil || latest != "v1.2.3" || sum != channelIndexSum {
		t.Fatalf("latest=%q sum=%q err=%v", latest, sum, err)
	}
	over := exact + "\n"
	if len(over) != 65537 {
		t.Fatalf("oversize length %d", len(over))
	}
	if _, _, err := parseChannelIndex(over, InstallChannelMain, "linux", "amd64"); err == nil {
		t.Fatal("expected oversize error")
	}
}

func channelIndexBody(tag, sum string) string {
	return "latest " + tag + "\nlinux-amd64 https://example.invalid/a " + sum + "\n"
}

func TestChannelIndex_Fetch(t *testing.T) {
	devBody := strings.Join([]string{
		"latest v1.2.3-dev.1",
		"darwin-arm64 https://example.invalid/other " + channelIndexOtherSum,
		"linux-amd64 https://example.invalid/bundle " + channelIndexSum,
		"",
	}, "\n")
	latest, sum, url, calls, err := fetchChannelIndexFixture(t, InstallChannelDev, "linux", "amd64", http.StatusOK, devBody)
	if err != nil || latest != "v1.2.3-dev.1" || sum != channelIndexSum || url != channelIndexDevURL || calls != 1 {
		t.Fatalf("latest=%q sum=%q url=%q calls=%d err=%v", latest, sum, url, calls, err)
	}

	mainBody := "latest v1.2.3\nlinux-amd64 https://example.invalid/bundle " + channelIndexSum + "\n"
	latest, sum, url, calls, err = fetchChannelIndexFixture(t, InstallChannelMain, "linux", "amd64", http.StatusOK, mainBody)
	if err != nil || latest != "v1.2.3" || sum != channelIndexSum || url != channelIndexStableURL || calls != 1 {
		t.Fatalf("main latest=%q sum=%q url=%q calls=%d err=%v", latest, sum, url, calls, err)
	}
}

func TestChannelIndex_FetchRejects(t *testing.T) {
	valid := channelIndexBody("v1.2.3-dev.1", channelIndexSum)
	for _, status := range []int{http.StatusCreated, http.StatusFound, http.StatusNotFound, http.StatusInternalServerError} {
		latest, sum, url, calls, err := fetchChannelIndexFixture(t, InstallChannelDev, "linux", "amd64", status, valid)
		if err == nil || latest != "" || sum != "" || url != channelIndexDevURL || calls != 1 {
			t.Fatalf("status %d latest=%q sum=%q url=%q calls=%d err=%v", status, latest, sum, url, calls, err)
		}
	}

	calls := 0
	client := &http.Client{Transport: channelIndexRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		t.Errorf("unexpected request %s", r.URL.String())
		return nil, io.ErrUnexpectedEOF
	})}
	if _, _, err := fetchChannelIndex(context.Background(), client, "stable", "linux", "amd64"); err == nil || calls != 0 {
		t.Fatalf("unknown channel err=%v calls=%d", err, calls)
	}

	latest, sum, _, calls, err := fetchChannelIndexFixture(t, InstallChannelDev, "darwin", "arm64", http.StatusOK, valid)
	if err == nil || latest != "" || sum != "" || calls != 1 {
		t.Fatalf("wrong platform latest=%q sum=%q calls=%d err=%v", latest, sum, calls, err)
	}
}

func TestChannelIndex_FetchSizeBoundary(t *testing.T) {
	body := channelIndexBody("v1.2.3", channelIndexSum)
	exact := body + "#" + strings.Repeat(".", channelIndexMaxBytes-len(body)-1)
	if len(exact) != channelIndexMaxBytes {
		t.Fatalf("boundary length %d", len(exact))
	}
	latest, sum, _, calls, err := fetchChannelIndexFixture(t, InstallChannelMain, "linux", "amd64", http.StatusOK, exact)
	if err != nil || latest != "v1.2.3" || sum != channelIndexSum || calls != 1 {
		t.Fatalf("latest=%q sum=%q calls=%d err=%v", latest, sum, calls, err)
	}
	latest, sum, _, calls, err = fetchChannelIndexFixture(t, InstallChannelMain, "linux", "amd64", http.StatusOK, exact+"\n")
	if err == nil || latest != "" || sum != "" || calls != 1 {
		t.Fatalf("oversize latest=%q sum=%q calls=%d err=%v", latest, sum, calls, err)
	}
}

func TestChannelIndex_FetchRefusesRedirect(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: channelIndexRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != channelIndexDevURL {
			t.Errorf("redirect target requested: %s", r.URL.String())
		}
		header := make(http.Header)
		header.Set("Location", "https://github.com/mihari-proxy/mihari/releases/download/v1.2.3-dev.1/SHA256SUMS.txt")
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(channelIndexBody("v1.2.3-dev.1", channelIndexSum))),
			Request:    r,
		}, nil
	})}
	latest, sum, err := fetchChannelIndex(context.Background(), client, InstallChannelDev, "linux", "amd64")
	if err == nil || latest != "" || sum != "" || calls != 1 {
		t.Fatalf("latest=%q sum=%q calls=%d err=%v", latest, sum, calls, err)
	}
	if client.CheckRedirect != nil {
		t.Fatal("fetch mutated CheckRedirect")
	}
}

func fetchChannelIndexFixture(t *testing.T, channel, goos, goarch string, status int, body string) (latest, sum, url string, calls int, err error) {
	t.Helper()
	client := &http.Client{Transport: channelIndexRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		url = r.URL.String()
		if r.Method != http.MethodGet {
			t.Errorf("method %s", r.Method)
		}
		if r.Header.Get("User-Agent") != "mihari" {
			t.Errorf("User-Agent %q", r.Header.Get("User-Agent"))
		}
		if r.URL.Hostname() == "github.com" || strings.HasSuffix(r.URL.Hostname(), ".github.com") || strings.Contains(r.URL.Hostname(), "githubusercontent.com") {
			t.Errorf("fetch requested GitHub: %s", r.URL.String())
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}
	latest, sum, err = fetchChannelIndex(context.Background(), client, channel, goos, goarch)
	if err != nil {
		latest, sum = "", ""
	}
	return latest, sum, url, calls, err
}

type channelIndexRoundTrip func(*http.Request) (*http.Response, error)

func (f channelIndexRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
