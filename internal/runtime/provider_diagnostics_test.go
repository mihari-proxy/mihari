package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/mihomo"
)

type providerDiagnosticTransport func(*http.Request) (*http.Response, error)

func (f providerDiagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestProviderDiagnostic_RealAdapterOwnerJSONAndReplay(t *testing.T) {
	cause := &os.PathError{Op: "open", Path: "/private/business-secret", Err: os.ErrPermission}
	calls := 0
	adapter := mihomo.NewClient("http://127.0.0.1", "controller-secret", &http.Client{Transport: providerDiagnosticTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, cause })})
	var output bytes.Buffer
	m := newTestManager(Options{Controller: adapter, DiagnosticReporter: businessJSONReporter(&output)})
	op := Operation{ID: "provider-failure", Source: "test"}
	for range 2 {
		err := m.UpdateRuleProvider(context.Background(), op, "business-secret")
		if !errors.Is(err, cause) || !diagnostics.AlreadyReported(err) {
			t.Fatalf("native provider cause lost: %v", err)
		}
	}
	assertBusinessFailureJSON(t, output.String(), op.ID, "rule_provider.refresh", "upstream_failure")
	if calls != 1 || m.Snapshot().Revision != 0 {
		t.Fatal("provider replay or failed revision changed")
	}
}
func TestProviderDiagnostic_UnsupportedNativeMutationKeepsContract(t *testing.T) {
	adapter := mihomo.NewClient("http://127.0.0.1", "controller-secret", &http.Client{Transport: providerDiagnosticTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 405, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("business-secret upstream configuration"))}, nil
	})})
	var output bytes.Buffer
	m := newTestManager(Options{Controller: adapter, DiagnosticReporter: businessJSONReporter(&output)})
	err := m.UpdateRuleProvider(context.Background(), Operation{ID: "unsupported", Source: "test"}, "business-secret")
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeUpstreamFailure || api.Message != "mihomo request failed" || api.Details["status"] != 405 || m.Snapshot().Revision != 0 {
		t.Fatalf("native unsupported contract changed: %v", err)
	}
	if strings.Contains(output.String(), "business-secret") || !strings.Contains(output.String(), `"operation":"rule_provider.refresh"`) {
		t.Fatalf("provider logs=%s", output.String())
	}
}
