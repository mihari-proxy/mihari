package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestProcessIdentityLookup_RejectsReusedPID(t *testing.T) {
	recorded := ProcessIdentity{PID: 42, BootID: "boot", StartUnix: 123, StartUsec: 456}
	for _, tc := range []struct {
		name   string
		change func(*ProcessIdentity)
	}{
		{"boot", func(id *ProcessIdentity) { id.BootID = "later-boot" }},
		{"seconds", func(id *ProcessIdentity) { id.StartUnix++ }},
		{"microseconds", func(id *ProcessIdentity) { id.StartUsec++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := recorded
			tc.change(&observed)
			alive, err := lookupProcessIdentity(context.Background(), recorded, func(_ context.Context, pid int) (ProcessIdentity, error) {
				if pid != recorded.PID {
					t.Fatal("queried a different process")
				}
				return observed, nil
			})
			var api protocol.APIError
			if alive || !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
				t.Fatalf("identity mismatch treated as a proven exit: alive=%v err=%v", alive, err)
			}
		})
	}
}

func TestProcessIdentityLookup_ExistingAndMissingPID(t *testing.T) {
	recorded := ProcessIdentity{PID: 42, BootID: "boot", StartUnix: 123, StartUsec: 456}
	for _, observed := range []ProcessIdentity{recorded, {}} {
		alive, err := lookupProcessIdentity(context.Background(), recorded, func(context.Context, int) (ProcessIdentity, error) { return observed, nil })
		if err != nil || alive != (observed.PID != 0) {
			t.Fatalf("identity lookup alive=%v err=%v", alive, err)
		}
	}
}
