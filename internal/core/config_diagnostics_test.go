package core

import (
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestConfigDiagnostic_TrustedPublicationKeepsRecoveryCauses(t *testing.T) {
	for _, mode := range []string{"read", "restore"} {
		t.Run(mode, func(t *testing.T) {
			f := NewTestTrustedFixture(t, t.TempDir())
			before := f.Content()
			candidate, err := f.Trusted.PrepareGenerated(context.Background(), []byte("new config"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := candidate.Close(); err != nil {
					t.Error(err)
				}
			}()
			first, second := errors.New("publication failure"), errors.New("recovery failure")
			f.FailConfigWriteAfter(1, first)
			if mode == "read" {
				f.files.readErrors = map[int]error{f.files.writes + 1: second}
			} else {
				f.FailConfigWriteAfter(2, second)
			}
			_, err = f.Trusted.Publish(context.Background(), candidate, candidate.Hash())
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || api.Message != "configuration publication recovery could not be confirmed" || api.Details["degraded"] != true {
				t.Fatalf("publication contract changed: %v", err)
			}
			if !errors.Is(err, first) || !errors.Is(err, second) {
				t.Error("publication/recovery cause missing")
			}
			if mode == "restore" && string(f.Content()) != string(before) {
				t.Error("restore write ordering changed")
			}
			if mode == "read" && string(f.Content()) != "new config" {
				t.Error("unconfirmed publication changed recovery policy")
			}
		})
	}
}
