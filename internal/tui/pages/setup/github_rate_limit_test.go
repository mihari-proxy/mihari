package setup

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestSetupGitHubRateLimit_ExplainsWaitingInsteadOfConnectivity(t *testing.T) {
	err := diagnostics.GitHubHTTPError(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "fetch mihomo release failed"}, &http.Response{StatusCode: 429}, &diagnostics.HTTPError{Status: 429, Operation: "core GET release", Body: "Too Many Requests"})
	m := &Model{}
	m.fail("Install core", err)
	if !strings.Contains(m.lastError, diagnostics.GitHubRateLimitSummary) || m.errorAdvice != "" || !strings.Contains(m.errorDetail, "core GET release") {
		t.Fatalf("error=%q advice=%q detail=%q", m.lastError, m.errorAdvice, m.errorDetail)
	}
}
