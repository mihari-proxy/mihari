package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOnboardingDTOsUseStableSafeJSON(t *testing.T) {
	complete := true
	revision := uint64(3)
	values := []any{
		OnboardingStatus{Schema: "mihari/v1", Revision: 3, Complete: true, ControllerAddr: "127.0.0.1:9090"},
		OnboardingUpdateRequest{OperationID: "setup-1", IfRevision: &revision, Complete: &complete},
	}
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil || strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "token") {
			t.Fatalf("json=%s err=%v", raw, err)
		}
	}
}
