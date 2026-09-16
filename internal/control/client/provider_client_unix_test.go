//go:build linux || darwin

package client

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/platform"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionTimeout_VerifiedUnixConstructorUsesLongBudget(t *testing.T) {
	for _, add := range []bool{false, true} {
		p := &sequenceProvider{value: "fixture"}
		c := WithCredentialProvider(platform.ControlLocator{}, p)
		c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) < SubscriptionMutationTimeout-time.Second {
				t.Error("Unix subscription used ordinary timeout")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"schema":"mihari/v1"}`))}, nil
		})
		if err := invokeSubscriptionMutation(context.Background(), c, add); err != nil {
			t.Fatal(err)
		}
		if p.calls != 1 || c.http.Timeout != 10*time.Second {
			t.Fatal("Unix credential or ordinary budget changed")
		}
	}
}

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
