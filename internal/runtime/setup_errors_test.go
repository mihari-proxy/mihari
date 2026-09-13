package runtime

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/geoip"
)

func TestGeoIPSetupFailure_SafeKnownCauseAndInternalDiagnostics(t *testing.T) {
	cause := &net.DNSError{Err: "lookup failed", Name: "private-token.example.test"}
	m := newTestManager(Options{GeoIP: &fakeGeoIPService{statusFunc: func() geoip.Status { return geoip.Status{} }}, PrepareGeoIP: func(context.Context) (GeoIPCandidate, error) { return nil, cause }})
	_, err := m.UpdateGeoIP(context.Background(), Operation{ID: "geoip-failure", Source: "setup"})
	var api protocol.APIError
	if !errors.As(err, &api) || !strings.Contains(api.Message, "DNS") {
		t.Fatal("known DNS failure reduced to generic internal error")
	}
	if strings.Contains(api.Message, "private-token") || !errors.Is(err, cause) {
		t.Fatal("diagnostic cause was leaked or lost")
	}
}
