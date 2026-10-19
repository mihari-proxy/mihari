package diagnostics

import (
	"errors"
	"net/http"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestGitHubHTTPError_RateLimitClassification(t *testing.T) {
	for _, tt := range []struct {
		name            string
		status          int
		remaining, body string
		limited         bool
	}{
		{"primary", 403, "", `{"message":"API rate limit exceeded for fixture IP."}`, true},
		{"secondary", 403, "", `{"message":"You have exceeded a secondary rate limit. Please wait."}`, true},
		{"raw text", 403, "", "API rate limit exceeded", true},
		{"exhausted", 403, "0", `{"message":"Forbidden"}`, true},
		{"too many", 429, "", "", true},
		{"permission", 403, "10", `{"message":"Resource not accessible by integration"}`, false},
		{"unrelated rate", 403, "", `{"message":"This resource has a rate limit configuration"}`, false},
		{"nested mention", 403, "", `{"message":"Forbidden","documentation_url":"https://example.invalid/API rate limit exceeded"}`, false},
		{"truncated after message", 403, "", `{"message":"API rate limit exceeded","extra":"unfinished`, true},
		{"server failure", 500, "0", "API rate limit exceeded", false},
		{"success", 200, "0", "API rate limit exceeded", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cause := errors.New("fixture body read failed")
			detail := &HTTPError{Operation: "fixture GET release", URL: "https://fixture.invalid/release", Phase: "response", Status: tt.status, Body: tt.body, Cause: cause}
			api := protocol.APIError{Code: protocol.CodeDataFailure, Message: "fetch release failed", Details: map[string]any{"status": tt.status}}
			response := &http.Response{StatusCode: tt.status, Header: http.Header{"X-Ratelimit-Remaining": []string{tt.remaining}, "Retry-After": []string{"invalid"}, "X-Ratelimit-Reset": []string{"9999999999"}}}
			err := GitHubHTTPError(api, response, detail)
			var got protocol.APIError
			if !errors.As(err, &got) {
				t.Fatal("missing API error")
			}
			want := api.Message
			if tt.limited {
				want = GitHubRateLimitSummary
			}
			if got.Message != want || got.Code != api.Code || got.Details["status"] != tt.status {
				t.Fatalf("API error = %+v, want message %q", got, want)
			}
			var retained *HTTPError
			if !errors.As(err, &retained) || retained != detail || !errors.Is(err, cause) {
				t.Fatal("original HTTP diagnostic or cause lost")
			}
			snapshot := Describe(t.Context(), Record{Err: err})
			if snapshot.Summary != want {
				t.Fatalf("summary = %q", snapshot.Summary)
			}
		})
	}
}

func TestGitHubHTTPError_IgnoresRetryTimingHeaders(t *testing.T) {
	for _, value := range []string{"", "120", "4102444800", "Wed, 21 Oct 2015 07:28:00 GMT", "invalid", "9999999999999999999999999"} {
		for _, status := range []int{403, 429} {
			response := &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{value}, "X-Ratelimit-Reset": []string{value}}}
			err := GitHubHTTPError(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "fetch failed"}, response, &HTTPError{Status: status, Body: `{"message":"Resource not accessible by integration"}`})
			want := "fetch failed"
			if status == 429 {
				want = GitHubRateLimitSummary
			}
			if err.Error() != want {
				t.Fatalf("status %d timing %q: %v", status, value, err)
			}
		}
	}
}
