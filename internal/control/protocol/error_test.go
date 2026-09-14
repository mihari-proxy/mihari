package protocol

import (
	"encoding/json"
	"errors"
	"io"
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

func TestAPIErrorCause_ExposesPublicAndInternalErrors(t *testing.T) {
	public := APIError{Code: CodeDataFailure, Message: "invalid payload", Details: map[string]any{"field": "payload"}}
	err := wrapAPIErrorCause(public, io.ErrUnexpectedEOF)
	var got APIError
	if !errors.As(err, &got) || got.Code != public.Code || got.Message != public.Message || got.Details["field"] != "payload" {
		t.Fatalf("public API error missing: %#v", got)
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) || err.Error() != public.Message {
		t.Fatalf("internal cause or safe message missing: %v", err)
	}
	children, ok := err.(interface{ Unwrap() []error })
	if !ok || len(children.Unwrap()) != 2 {
		t.Fatalf("diagnostic traversal cannot inspect both errors: %T", err)
	}
}

func TestSystemProxyErrorCodesExist(t *testing.T) {
	tests := []struct {
		code ErrorCode
		want string
	}{
		{CodeSystemProxyConflict, "system_proxy_conflict"},
		{CodeSystemProxyNotOwned, "system_proxy_not_owned"},
	}
	for _, test := range tests {
		if string(test.code) != test.want {
			t.Fatalf("code=%q want=%q", test.code, test.want)
		}
		env := NewError(test.code, "test", nil)
		raw, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		if !jsonContainsCode(raw, test.want) {
			t.Fatalf("envelope missing code %q: %s", test.want, raw)
		}
	}
}

func TestTunConflictCodeExists(t *testing.T) {
	const want = "tun_conflict"
	if string(CodeTunConflict) != want {
		t.Fatalf("code=%q want=%q", CodeTunConflict, want)
	}
	env := NewError(CodeTunConflict, "tun conflict", nil)
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonContainsCode(raw, want) {
		t.Fatalf("envelope missing code %q: %s", want, raw)
	}
}

func jsonContainsCode(raw []byte, code string) bool {
	var env ErrorEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return false
	}
	return string(env.Error.Code) == code
}
