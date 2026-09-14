package update

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

// OfficialReleaseSource obtains independent fixed-tag evidence from the project
// release origin. Client permits an injected transport; URLs are never inputs.
type OfficialReleaseSource struct {
	Client *http.Client
	// Reporter borrows an existing diagnostic outlet; nil skips close warnings.
	Reporter diagnostics.Reporter
}

func (s OfficialReleaseSource) client() *http.Client {
	var client http.Client
	if s.Client != nil {
		client = *s.Client
	}
	if client.Timeout == 0 {
		client.Timeout = 2 * time.Minute
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || !officialAssetURL(req.URL) {
			return protocol.APIError{Code: protocol.CodePermissionDenied, Message: "untrusted release redirect"}
		}
		return nil
	}
	return &client
}

func officialAssetURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch u.Hostname() {
	case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	default:
		return false
	}
}

// Checksum returns the unique digest in the official fixed-tag manifest.
func (s OfficialReleaseSource) Checksum(ctx context.Context, tag, asset string) (string, error) {
	raw, err := s.Download(ctx, tag, checksumAssetName, maxChecksumManifestSize)
	if err != nil {
		return "", err
	}
	digest, err := parseChecksumManifest(raw, asset)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest[:]), nil
}

// Download retrieves a bounded asset from the immutable release URL. Callers
// must verify its independent checksum before trusting executable/resource bytes.
func (s OfficialReleaseSource) Download(ctx context.Context, tag, asset string, limit int64) (result []byte, resultErr error) {
	if _, ok := parseCanonicalTag(tag); !ok {
		return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid fixed release tag"}
	}
	if asset == "" || strings.ContainsAny(asset, "/\\?#\x00") || limit <= 0 {
		return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid release asset"}
	}
	address := "https://github.com/" + defaultRepo + "/releases/download/" + tag + "/" + asset
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "mihari")
	response, err := s.client().Do(request)
	if err != nil {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download official release evidence"}, updateHTTPTransport(address, err))
	}
	defer closeUpdateResponse(ctx, response, address, &resultErr, s.Reporter)
	if response.StatusCode != http.StatusOK {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download official release evidence"}, updateHTTPStatus(response, address))
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid official release asset"}, updateHTTPRead(response, address, err))
	}
	if int64(len(raw)) > limit {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid official release asset"}
	}
	return raw, nil
}
