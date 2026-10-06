package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

// channelIndexMaxBytes is the fixed channel index body limit. A longer text
// is rejected before any line is trusted.
const channelIndexMaxBytes = 65536

const (
	stableChannelIndexURL = "https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt"
	devChannelIndexURL    = "https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari-dev/index.txt"
)

var (
	canonicalStableTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	canonicalDevTag    = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-dev\.(0|[1-9][0-9]*)$`)
	channelPlatformKey = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)
)

// channelIndexURL returns the fixed HTTPS channel index for main or dev.
func channelIndexURL(channel string) (string, error) {
	switch channel {
	case InstallChannelMain:
		return stableChannelIndexURL, nil
	case InstallChannelDev:
		return devChannelIndexURL, nil
	default:
		return "", fmt.Errorf("mihari channel must be main or dev")
	}
}

// parseChannelIndex returns the latest tag and the sha256 of the goos-goarch
// bundle. main accepts only vX.Y.Z; dev accepts only vX.Y.Z-dev.N. text longer
// than 65536 bytes is rejected.
func parseChannelIndex(text, channel, goos, goarch string) (string, string, error) {
	if channel != InstallChannelMain && channel != InstallChannelDev {
		return "", "", fmt.Errorf("mihari channel must be main or dev")
	}
	if len(text) > channelIndexMaxBytes {
		return "", "", fmt.Errorf("channel index exceeds 65536 bytes")
	}
	want := goos + "-" + goarch
	seenPlatform := map[string]struct{}{}
	var latest, sum string
	haveLatest := false
	havePlatform := false
	for _, line := range strings.Split(text, "\n") {
		fields, ok := channelIndexFields(line)
		if !ok {
			continue
		}
		key := fields[0]
		if key == "latest" {
			if haveLatest {
				return "", "", fmt.Errorf("duplicate channel index key %q", key)
			}
			tag := ""
			if len(fields) == 2 {
				tag = fields[1]
			}
			if err := canonicalChannelTag(channel, tag); err != nil {
				return "", "", err
			}
			latest = tag
			haveLatest = true
			continue
		}
		if key != want && !channelPlatformKey.MatchString(key) {
			continue
		}
		if _, dup := seenPlatform[key]; dup {
			return "", "", fmt.Errorf("duplicate channel index key %q", key)
		}
		seenPlatform[key] = struct{}{}
		if len(fields) != 3 || !validSHA256(fields[2]) {
			return "", "", fmt.Errorf("channel index sha256 must be 64 lowercase hex")
		}
		if key == want {
			sum = fields[2]
			havePlatform = true
		}
	}
	if !haveLatest {
		return "", "", fmt.Errorf("channel index has no latest release")
	}
	if !havePlatform {
		return "", "", fmt.Errorf("channel index has no package for %s", want)
	}
	return latest, sum, nil
}

// channelIndexFields returns the fields of one index line. Empty lines and
// lines whose first non-space content starts with "#" or "//" are skipped.
func channelIndexFields(line string) ([]string, bool) {
	trimmed := strings.TrimLeft(strings.TrimRight(line, "\r"), " \t")
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
		return nil, false
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return nil, false
	}
	return fields, true
}

// fetchChannelIndex GETs channelIndexURL and returns the latest tag and the
// sha256 for goos-goarch. The body is limited to 65536 bytes. Any status
// other than 200 fails. A nil client uses the default transport. A non-nil
// client is copied so its transport is used, the caller is not mutated, and
// a redirect cannot replace the fixed index URL.
func fetchChannelIndex(ctx context.Context, client *http.Client, channel, goos, goarch string) (latest, sum string, err error) {
	address, err := channelIndexURL(channel)
	if err != nil {
		return "", "", err
	}
	if client == nil {
		client = &http.Client{}
	}
	httpClient := *client
	httpClient.CheckRedirect = refuseChannelIndexRedirect
	if httpClient.Timeout == 0 {
		httpClient.Timeout = 2 * time.Minute
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("User-Agent", "mihari")
	response, err := httpClient.Do(request)
	if err != nil {
		return "", "", diagnostics.Wrap(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "fetch channel index"}, err)
	}
	if response.Body == nil {
		return "", "", protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "fetch channel index"}
	}
	defer func() {
		closeErr := response.Body.Close()
		if closeErr == nil {
			return
		}
		latest, sum = "", ""
		if err == nil {
			err = diagnostics.Wrap(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "fetch channel index"}, closeErr)
			return
		}
		err = errors.Join(err, closeErr)
	}()
	if response.StatusCode != http.StatusOK {
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, channelIndexMaxBytes+1))
		detail := &diagnostics.HTTPError{
			Operation:     "channel index GET",
			URL:           address,
			Phase:         "response",
			Status:        response.StatusCode,
			Body:          diagnostics.HTTPBody(raw),
			BodyTruncated: len(raw) > channelIndexMaxBytes,
			Cause:         readErr,
		}
		return "", "", diagnostics.Wrap(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "fetch channel index"}, detail)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, channelIndexMaxBytes+1))
	if err != nil {
		return "", "", diagnostics.Wrap(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "fetch channel index"}, err)
	}
	if len(raw) > channelIndexMaxBytes {
		return "", "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "channel index exceeds 65536 bytes"}
	}
	latest, sum, err = parseChannelIndex(string(raw), channel, goos, goarch)
	if err != nil {
		return "", "", diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid channel index"}, err)
	}
	return latest, sum, nil
}

func refuseChannelIndexRedirect(req *http.Request, _ []*http.Request) error {
	location := ""
	if req != nil && req.URL != nil {
		location = req.URL.String()
	}
	return fmt.Errorf("channel index redirect refused: %s", location)
}

func canonicalChannelTag(channel, tag string) error {
	switch channel {
	case InstallChannelMain:
		if canonicalStableTag.MatchString(tag) {
			return nil
		}
		return fmt.Errorf("main index latest must be vX.Y.Z")
	case InstallChannelDev:
		if canonicalDevTag.MatchString(tag) {
			return nil
		}
		return fmt.Errorf("dev index latest must be vX.Y.Z-dev.N")
	default:
		return fmt.Errorf("mihari channel must be main or dev")
	}
}
