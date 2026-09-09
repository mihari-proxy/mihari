package runtime

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
)

func TestNativeProviderRefresh_MissingControllerDoesNotAdvanceRevision(t *testing.T) {
	m := New(Options{})
	before := m.store.Load().Revision
	err := m.RefreshProvider(context.Background(), Operation{ID: "native-missing", Source: "test"}, "rules")
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatal(err)
	}
	if m.store.Load().Revision != before {
		t.Fatal("failed provider operation advanced revision")
	}
}
