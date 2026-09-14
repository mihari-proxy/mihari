package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestOwnerScan_StartupCacheErrorPreservesPrivateCause(t *testing.T) {
	cause := errors.New("read private cache fixture")
	err := startupCacheError(cause)
	var api protocol.APIError
	if !errors.Is(err, cause) || !errors.As(err, &api) || api.Code != protocol.CodeDataFailure {
		t.Fatalf("startup cache classification/cause=%v", err)
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("public startup cache error exposed cause: %v", err)
	}
}
