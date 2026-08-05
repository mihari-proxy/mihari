package subscription

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
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
}

type FetchResult struct {
	Content      []byte
	ETag         string
	LastModified string
	// Userinfo is the raw subscription-userinfo response header when present.
	Userinfo    string
	NotModified bool
}

type Downloader struct {
	Client   *http.Client
	MaxBytes int64
}

func NewDownloader(client *http.Client) *Downloader {
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return errors.New("too many redirects")
				}
				if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && request.URL.Scheme != "https" {
					return errors.New("HTTPS redirect downgrade")
				}
				return nil
			},
		}
	}
	return &Downloader{Client: client, MaxBytes: defaultMaxSubscriptionBytes}
}

func (d *Downloader) Fetch(ctx context.Context, input FetchRequest) (FetchResult, error) {
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
	response, err := d.Client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return FetchResult{}, ctx.Err()
		}
		return FetchResult{}, protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "subscription download failed"}
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
