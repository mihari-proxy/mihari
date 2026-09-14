package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestOwnerScan_ServiceErrorMappingPreservesPrivateCause(t *testing.T) {
	cause := errors.New("SCM private path C:\\fixture\\mihari.exe")
	err := mapServiceError(cause)
	var api protocol.APIError
	if !errors.Is(err, cause) || !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("service classification/cause=%v", err)
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("public service error exposed cause: %v", err)
	}
}

func TestOwnerScan_ServiceStagingPreservesCauseWithoutPublicDetails(t *testing.T) {
	cause := errors.New("copy C:\\private\\source.exe: access denied")
	manager := New(Options{Executable: "fixture.exe"})
	manager.stageBinary = func(string) (string, error) { return "", cause }
	_, err := manager.stageServiceBinary()
	var api protocol.APIError
	if !errors.Is(err, cause) || !errors.As(err, &api) || api.Code != protocol.CodeDataFailure {
		t.Fatalf("staging classification/cause=%v", err)
	}
	if api.Details["error"] != "copy failed" || strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("public staging error exposed cause: %#v / %v", api.Details, err)
	}
}
