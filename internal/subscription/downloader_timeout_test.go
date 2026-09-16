package subscription

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type deadlineSubscriptionBody struct{ ctx context.Context }

func (b deadlineSubscriptionBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func TestDownloaderAuto_BodyTimeoutFallsBack(t *testing.T) {
	for _, parentCancel := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		var proxyBody *originalSubscriptionBody
		proxy := &http.Client{Timeout: 20 * time.Millisecond, Transport: originalSubscriptionTransport(func(r *http.Request) (*http.Response, error) {
			proxyBody = &originalSubscriptionBody{Reader: io.MultiReader(strings.NewReader("partial"), deadlineSubscriptionBody{r.Context()})}
			if parentCancel {
				cancel()
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: proxyBody}, nil
		})}
		calls := 0
		directBody := &originalSubscriptionBody{Reader: strings.NewReader("proxies: []\n")}
		direct := &http.Client{Transport: originalSubscriptionTransport(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: directBody}, nil
		})}
		d := &Downloader{proxy: proxy, direct: direct}
		result, err := d.Fetch(ctx, FetchRequest{URL: "http://fixture.invalid/sub", Mode: ProxyModeAuto})
		cancel()
		if parentCancel {
			if err == nil || calls != 0 || result.FellBack {
				t.Fatalf("parent cancellation retried: result=%+v calls=%d err=%v", result, calls, err)
			}
		} else if err != nil || calls != 1 || !result.FellBack || string(result.Content) != "proxies: []\n" || directBody.closes != 1 {
			t.Fatalf("body timeout did not recover: result=%+v calls=%d err=%v", result, calls, err)
		}
		if proxyBody.closes != 1 {
			t.Fatalf("proxy closes = %d", proxyBody.closes)
		}
	}
}

func TestDownloaderAuto_BodyErrorFallbackClassification(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		mode     string
		cause    error
		fallback bool
	}{
		{"timeout", 200, ProxyModeAuto, context.DeadlineExceeded, true},
		{"unexpected EOF", 200, ProxyModeAuto, io.ErrUnexpectedEOF, false},
		{"other", 200, ProxyModeAuto, errors.New("fixture body failure"), false},
		{"body reset", 200, ProxyModeAuto, errors.New("connection reset"), false},
		{"HTTP failure", 503, ProxyModeAuto, context.DeadlineExceeded, false},
		{"proxy only", 200, ProxyModeProxy, context.DeadlineExceeded, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxyBody := &originalSubscriptionBody{Reader: originalSubscriptionReadFailure{tc.cause}}
			calls := 0
			d := &Downloader{
				proxy: &http.Client{Transport: originalSubscriptionTransport(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: proxyBody}, nil
				})},
				direct: &http.Client{Transport: originalSubscriptionTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("direct also failed") })},
			}
			result, err := d.Fetch(context.Background(), FetchRequest{URL: "http://fixture.invalid/sub?token=fixture", Mode: tc.mode})
			if err == nil || result.FellBack || (calls == 1) != tc.fallback || proxyBody.closes != 1 {
				t.Fatalf("calls=%d result=%+v closes=%d err=%v", calls, result, proxyBody.closes, err)
			}
			if !tc.fallback {
				var detail *diagnostics.HTTPError
				if !errors.Is(err, tc.cause) || !errors.As(err, &detail) {
					t.Fatal("original body cause missing")
				}
				if tc.status == 200 && detail.Phase != "read" {
					t.Fatal("read phase lost")
				}
			}
			if strings.Contains(err.Error(), "fixture") {
				t.Fatal("public error leaked URL/cause")
			}
		})
	}
}
