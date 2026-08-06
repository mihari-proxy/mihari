package elevate

import (
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestRequireElevatedRejectsWhenNotAdmin(t *testing.T) {
	prev := Check
	t.Cleanup(func() { Check = prev })
	Check = func() bool { return false }
	err := RequireElevated()
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodePermissionDenied {
		t.Fatalf("err=%v", err)
	}
}

func TestRequireElevatedAllowsWhenAdmin(t *testing.T) {
	prev := Check
	t.Cleanup(func() { Check = prev })
	Check = func() bool { return true }
	if err := RequireElevated(); err != nil {
		t.Fatal(err)
	}
}

func TestIsElevatedUsesChecker(t *testing.T) {
	prev := Check
	t.Cleanup(func() { Check = prev })
	Check = func() bool { return true }
	if !IsElevated() {
		t.Fatal("expected elevated")
	}
	Check = func() bool { return false }
	if IsElevated() {
		t.Fatal("expected not elevated")
	}
}
