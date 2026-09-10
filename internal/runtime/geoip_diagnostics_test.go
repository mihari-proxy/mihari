package runtime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/geoip"
)

func TestGeoIPDiagnostic_PrepareCommitAndReplayJSON(t *testing.T) {
	for _, stage := range []string{"prepare", "commit"} {
		t.Run(stage, func(t *testing.T) {
			cause := &os.PathError{Op: "open", Path: "/private/business-secret", Err: os.ErrPermission}
			failure := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "geoip update failed"}, cause)
			candidate := &fakeGeoIPCandidate{valid: true, identity: "pair"}
			if stage == "commit" {
				candidate.commitErr = failure
			}
			var output bytes.Buffer
			m := newTestManager(Options{GeoIP: &fakeGeoIPService{}, DiagnosticReporter: businessJSONReporter(&output), PrepareGeoIP: func(context.Context) (GeoIPCandidate, error) {
				if stage == "prepare" {
					return nil, failure
				}
				return candidate, nil
			}})
			op := Operation{ID: "geoip-" + stage, Source: "test"}
			for range 2 {
				_, err := m.UpdateGeoIP(context.Background(), op)
				if !errors.Is(err, cause) || !diagnostics.AlreadyReported(err) {
					t.Fatalf("cause/marker lost: %v", err)
				}
			}
			assertBusinessFailureJSON(t, output.String(), op.ID, "geoip.update", "data_failure")
			if stage == "commit" && (candidate.commits != 1 || candidate.cleanups != 1) {
				t.Fatal("candidate replay/cleanup changed")
			}
		})
	}
}
func TestGeoIPDiagnostic_StaleCandidateRemainsDebugAndHealthUnchanged(t *testing.T) {
	var output bytes.Buffer
	service := &fakeGeoIPService{}
	candidate := &fakeGeoIPCandidate{valid: true, identity: "stale", commitErr: geoip.ErrStaleCandidate}
	m := newTestManager(Options{GeoIP: service, DiagnosticReporter: businessJSONReporter(&output), PrepareGeoIP: func(context.Context) (GeoIPCandidate, error) { return candidate, nil }})
	_, err := m.UpdateGeoIP(context.Background(), Operation{ID: "geoip-stale", Source: "test"})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict || service.recordedError || m.Snapshot().Revision != 0 {
		t.Fatalf("stale behavior changed: %v", err)
	}
	if !strings.Contains(output.String(), `"level":"DEBUG"`) || strings.Contains(output.String(), `"level":"ERROR"`) {
		t.Fatalf("stale logs=%s", output.String())
	}
}
