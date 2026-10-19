package diagnostics

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// GitHubRateLimitSummary is the controlled explanation for a confirmed GitHub limit.
const GitHubRateLimitSummary = "GitHub rate limit exceeded. Please try again later."

// GitHubHTTPError maps failures only at known GitHub resource boundaries.
// It preserves the established API classification and original HTTP diagnostic.
func GitHubHTTPError(api protocol.APIError, response *http.Response, detail *HTTPError) error {
	if response != nil && GitHubRateLimited(response.StatusCode, response.Header, detail.Body) {
		original := Wrap(api, detail)
		api.Message = GitHubRateLimitSummary
		return Wrap(api, original)
	}
	return Wrap(api, detail)
}

// GitHubRateLimited recognizes limits only for callers fetching fixed GitHub resources.
// Retry timing headers are deliberately ignored; this function does not schedule retries.
func GitHubRateLimited(status int, header http.Header, body string) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	if status != http.StatusForbidden {
		return false
	}
	if strings.TrimSpace(header.Get("X-RateLimit-Remaining")) == "0" {
		return true
	}
	message := strings.TrimSpace(body)
	if strings.HasPrefix(message, "{") {
		message = githubResponseMessage(message)
	}
	message = strings.ToLower(message)
	for _, phrase := range []string{"api rate limit exceeded", "exceeded a secondary rate limit", "exceeded the secondary rate limit", "secondary rate limit exceeded"} {
		if strings.Contains(message, phrase) {
			return true
		}
	}
	return false
}

// Read only the top-level message so a later body collection limit cannot hide
// an already captured explanation. Other fields never supply classification text.
func githubResponseMessage(body string) string {
	decoder := json.NewDecoder(strings.NewReader(body))
	if _, err := decoder.Token(); err != nil {
		return ""
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return ""
		}
		if key == "message" {
			var message string
			if decoder.Decode(&message) != nil {
				return ""
			}
			return message
		}
		var ignored json.RawMessage
		if decoder.Decode(&ignored) != nil {
			return ""
		}
	}
	return ""
}
