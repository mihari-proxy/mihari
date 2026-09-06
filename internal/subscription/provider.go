package subscription

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// ProviderDownloader retrieves a typed source through the subscription transport policy.
type ProviderDownloader interface {
	Download(context.Context, ProviderSpec, string) ([]byte, error)
}

// Download retrieves a provider source with its managed limits and headers.
func (d *Downloader) Download(ctx context.Context, spec ProviderSpec, mode string) ([]byte, error) {
	limit := int64(16 << 20)
	if spec.MaxBytes > 0 && spec.MaxBytes < limit {
		limit = spec.MaxBytes
	}
	return d.downloadManaged(ctx, spec, mode, limit)
}
func (d *Downloader) downloadManaged(ctx context.Context, spec ProviderSpec, mode string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if mode != ProxyModeDirect && mode != ProxyModeProxy && mode != ProxyModeAuto {
		return nil, dataError("invalid provider transport")
	}
	order := d.orderFor(mode)
	if len(order) == 0 {
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "no proxy configured"}
	}
	var last error
	for _, selected := range order {
		client := *selected
		client.Timeout = 30 * time.Second
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("provider redirect limit")
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("provider redirect downgrade")
			}
			return nil
		}
		content, err := downloadProvider(ctx, &client, spec, limit)
		if err == nil {
			return content, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		last = err
		if !isFallbackable(err) {
			break
		}
	}
	return nil, toAPIError(last)
}

func downloadProvider(ctx context.Context, client *http.Client, spec ProviderSpec, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.URL, nil)
	if err != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.Host == "" {
		return nil, dataError("invalid provider source")
	}
	for name, values := range spec.Header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, networkFailureError{cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "provider source returned an unsuccessful response"}
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "read provider response"}
	}
	if int64(len(content)) > limit {
		return nil, dataError("provider source exceeds size limit")
	}
	return content, nil
}
