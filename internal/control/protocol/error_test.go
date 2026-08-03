package protocol

import (
	"encoding/json"
	"testing"
)

func TestNewErrorEnvelope(t *testing.T) {
	env := NewError(CodeDaemonUnavailable, "daemon is unavailable", map[string]any{"endpoint": "local"})
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"mihari.error/v1","error":{"code":"daemon_unavailable","message":"daemon is unavailable","details":{"endpoint":"local"}}}`
	if string(b) != want {
		t.Fatalf("got %s want %s", b, want)
	}
}

func TestAPIErrorImplementsError(t *testing.T) {
	err := APIError{Code: CodeRevisionConflict, Message: "state changed"}
	if err.Error() != "state changed" {
		t.Fatalf("got %q", err.Error())
	}
}
