//go:build linux || darwin

package client

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
	"testing"
)

func TestProviderClient_UnixClassifications(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code protocol.ErrorCode
	}{
		{platform.ErrControlData, protocol.CodeDataFailure}, {platform.ErrUnsafeComponent, protocol.CodePermissionDenied}, {platform.ErrIdentityMismatch, protocol.CodePermissionDenied}, {platform.ErrLeaseConflict, protocol.CodeInvalidState}, {transport.ErrEndpointOccupied, protocol.CodeInvalidState},
	} {
		p := &sequenceProvider{err: tc.err}
		c := WithCredentialProvider(platform.ControlLocator{}, p)
		_, err := c.Status(context.Background())
		assertControlCode(t, err, tc.code)
		if p.calls != 1 {
			t.Fatal("provider not invoked once")
		}
	}
}

func TestProviderClient_ValidatesLocatorBeforeSending(t *testing.T) {
	l := platform.ControlLocator{Mode: platform.PrivateMode, ExpectedOwner: uint32(os.Geteuid()), BaseDir: "relative", Endpoint: "relative", Credential: "relative"}
	p := &sequenceProvider{value: "fixture"}
	c := WithCredentialProvider(l, p)
	_, err := c.Status(context.Background())
	assertControlCode(t, err, protocol.CodeInvalidArgument)
	if p.calls != 1 {
		t.Fatal("provider not called")
	}
}
