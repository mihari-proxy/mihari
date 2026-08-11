package subscription

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const (
	defaultMaxSubscriptionBytes = 16 << 20
	// Many providers only return a Clash YAML document when the User-Agent looks
	// like a Clash client (same approach as zashhomo). A generic agent often
	// receives base64 share-link lists instead.
	subscriptionUserAgent = "clash.meta/mihari"
)

type FetchRequest struct {
	URL          string
	ETag         string
	LastModified string
	// Mode selects the transport for this fetch. See ProxyMode* constants.
	Mode string
}

type FetchResult struct {
	Content      []byte
	ETag         string
	LastModified string
	// Userinfo is the raw subscription-userinfo response header when present.
	Userinfo    string
	NotModified bool
	// FellBack is true when the result came from a non-preferred client: in auto
	// mode the proxy failed at the network layer and the direct client succeeded.
	FellBack bool
}

// Downloader fetches subscription documents over HTTP. The direct client is
// always present; the proxy client is optional and backs proxy/auto modes.
type Downloader struct {
	direct   *http.Client
	proxy    *http.Client
	MaxBytes int64
}

// DownloaderOptions configures a Downloader. Client is the direct transport; a
// default 30s client is used when nil. ProxyURL enables proxy/auto modes by
// routing fetches through the mihomo mixed-port.
type DownloaderOptions struct {
	Client   *http.Client
	ProxyURL *url.URL
	MaxBytes int64
}

func NewDownloader(opts DownloaderOptions) *Downloader {
	direct := opts.Client
	if direct == nil {
		direct = newHTTPClient(nil)
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxSubscriptionBytes
	}
	var proxyClient *http.Client
	if opts.ProxyURL != nil {
		proxyClient = newHTTPClient(opts.ProxyURL)
	}
	return &Downloader{direct: direct, proxy: proxyClient, MaxBytes: maxBytes}
}

// newHTTPClient builds a 30s HTTP client with the shared redirect policy. When
// proxyURL is non-nil every request is routed through it (the mihomo mixed-port).
func newHTTPClient(proxyURL *url.URL) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: redirectPolicy}
}

func redirectPolicy(request *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return errors.New("too many redirects")
	}
	if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && request.URL.Scheme != "https" {
		return errors.New("HTTPS redirect downgrade")
	}
	return nil
}

// networkFailureError marks a client.Do transport failure. It is the only error
// isFallbackable recognizes, so it is the sole candidate for auto-mode retry.
// Fetch converts it back to a protocol.APIError before returning to callers.
type networkFailureError struct{ cause error }

func (e networkFailureError) Error() string { return "subscription download failed" }
func (e networkFailureError) Unwrap() error { return e.cause }

func (d *Downloader) Fetch(ctx context.Context, input FetchRequest) (FetchResult, error) {
	order := d.orderFor(input.Mode)
	if len(order) == 0 {
		return FetchResult{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "no proxy configured"}
	}
	var last error
	for index, client := range order {
		result, err := d.do(ctx, input, client)
		if err == nil {
			if index > 0 {
				result.FellBack = true
			}
			return result, nil
		}
		last = err
		if ctx.Err() != nil {
			return FetchResult{}, ctx.Err()
		}
		// Retry only on network-layer failures, and only while a client remains.
		if !isFallbackable(err) || index == len(order)-1 {
			break
		}
	}
	return FetchResult{}, toAPIError(last)
}

// toAPIError normalizes a fetch error for callers: a networkFailureError becomes
// CodeNetworkFailure so the control protocol surfaces the right status.
func toAPIError(err error) error {
	var netFail networkFailureError
	if errors.As(err, &netFail) {
		return protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "subscription download failed"}
	}
	return err
}

// orderFor returns the client sequence for a fetch mode. An empty result means
// the mode requires a proxy that was never configured.
func (d *Downloader) orderFor(mode string) []*http.Client {
	switch mode {
	case ProxyModeProxy:
		if d.proxy == nil {
			return nil
		}
		return []*http.Client{d.proxy}
	case ProxyModeAuto:
		if d.proxy == nil {
			return nil
		}
		return []*http.Client{d.proxy, d.direct}
	default:
		// ProxyModeDirect (the zero value) and any unrecognized value fetch directly.
		return []*http.Client{d.direct}
	}
}

// do performs one fetch attempt against a single client.
func (d *Downloader) do(ctx context.Context, input FetchRequest, client *http.Client) (FetchResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, input.URL, nil)
	if err != nil {
		return FetchResult{}, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid subscription URL"}
	}
	request.Header.Set("User-Agent", subscriptionUserAgent)
	request.Header.Set("Accept", "application/yaml, text/yaml, text/plain, application/octet-stream")
	if input.ETag != "" {
		request.Header.Set("If-None-Match", input.ETag)
	}
	if input.LastModified != "" {
		request.Header.Set("If-Modified-Since", input.LastModified)
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return FetchResult{}, ctx.Err()
		}
		return FetchResult{}, networkFailureError{cause: err}
	}
	defer response.Body.Close()
	result := FetchResult{
		ETag:         response.Header.Get("ETag"),
		LastModified: response.Header.Get("Last-Modified"),
		// Go canonicalizes the header key; providers send "subscription-userinfo".
		Userinfo: strings.TrimSpace(response.Header.Get("Subscription-Userinfo")),
	}
	if response.StatusCode == http.StatusNotModified {
		result.NotModified = true
		return result, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return FetchResult{}, protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "subscription provider returned an unsuccessful response", Details: map[string]any{"status": response.StatusCode}}
	}
	limit := d.MaxBytes
	if limit <= 0 {
		limit = defaultMaxSubscriptionBytes
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return FetchResult{}, protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "read subscription response"}
	}
	if int64(len(content)) > limit {
		return FetchResult{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "subscription document is too large"}
	}
	result.Content = content
	return result, nil
}

// isFallbackable reports whether a fetch error is a network-layer failure that
// auto mode should retry against the next client (proxy→direct). Only transport
// failures (timeout, connection refused, connection reset) qualify; HTTP status
// errors, invalid URLs, and oversized responses do not, since direct would fail
// for the same reason and the retry only wastes time.
func isFallbackable(err error) bool {
	var netFail networkFailureError
	if !errors.As(err, &netFail) {
		return false
	}
	cause := netFail.cause
	var timeout net.Error
	if errors.As(cause, &timeout) && timeout.Timeout() {
		return true
	}
	if errors.Is(cause, syscall.ECONNREFUSED) || errors.Is(cause, syscall.ECONNRESET) {
		return true
	}
	// Fall back to substring matching: errno unwrapping is unreliable when the
	// transport wraps the dial through a proxy ("proxyconnect") and Windows
	// phrases a refused connection as "actively refused".
	message := strings.ToLower(cause.Error())
	for _, marker := range []string{"connection refused", "actively refused", "connection reset"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
