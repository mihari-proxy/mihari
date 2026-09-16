package diagnostics

import (
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestDiagnostic_WrapPreservesSnapshotAndCause(t *testing.T) {
	cause := errors.New("token=fixture-original-cause")
	snapshot := &protocol.Diagnostic{ID: "instance:1", Detail: cause.Error(), State: protocol.DiagnosticAvailable}
	err := Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "load credential", Diagnostic: snapshot}, cause)
	var api protocol.APIError
	if !errors.As(err, &api) || api.Diagnostic == nil || *api.Diagnostic != *snapshot {
		t.Fatalf("wrapped diagnostic lost: %#v", api)
	}
	if !errors.Is(err, cause) || err.Error() != "load credential" {
		t.Fatal("diagnostic attachment changed cause traversal or summary")
	}
}
