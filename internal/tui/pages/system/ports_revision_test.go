package system

import (
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type revisionPortsClient struct {
	Client
	status      protocol.OnboardingStatus
	requests    []protocol.OnboardingUpdateRequest
	beforeWrite func(*revisionPortsClient)
	readErr     error
	writeErr    error
}

func (c *revisionPortsClient) Onboarding(ctx context.Context) (protocol.OnboardingStatus, error) {
	if err := ctx.Err(); err != nil {
		return protocol.OnboardingStatus{}, err
	}
	return c.status, c.readErr
}

func (c *revisionPortsClient) UpdateOnboarding(_ context.Context, request protocol.OnboardingUpdateRequest) (protocol.OnboardingStatus, error) {
	c.requests = append(c.requests, request)
	if c.beforeWrite != nil {
		c.beforeWrite(c)
	}
	if c.writeErr != nil {
		return protocol.OnboardingStatus{}, c.writeErr
	}
	if request.IfRevision == nil || *request.IfRevision != c.status.Revision {
		return protocol.OnboardingStatus{}, protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "state revision changed"}
	}
	if request.MixedAddr != nil {
		c.status.MixedAddr = *request.MixedAddr
	}
	if request.ControllerAddr != nil {
		c.status.ControllerAddr = *request.ControllerAddr
	}
	if request.WebAddr != nil {
		c.status.WebAddr = *request.WebAddr
	}
	c.status.Revision++
	c.status.RestartRequired = true
	return c.status, nil
}

func mixedPortIntent(t *testing.T) (*Model, *revisionPortsClient, ui.ActionIntentMsg) {
	t.Helper()
	m := portsModel(t)
	c := &revisionPortsClient{Client: m.client, status: m.onboarding}
	m.client = c
	m.listenFree = func(string) bool { return true }
	m.editID = rowMixed
	m.editInput.SetValue("127.0.0.1:19190")
	return m, c, m.confirmPortEdit()().(ui.ActionIntentMsg)
}

func TestSystemPorts_UnrelatedRevisionBeforeExecution(t *testing.T) {
	_, c, intent := mixedPortIntent(t)
	c.status.Revision++ // A subscription refresh completed after the edit began.
	result := intent.Execute().(portsApplyResultMsg)
	if result.err != nil || c.status.MixedAddr != "127.0.0.1:19190" {
		t.Fatalf("result=%+v status=%+v", result, c.status)
	}
	if len(c.requests) != 1 || c.requests[0].ControllerAddr != nil || c.requests[0].WebAddr != nil {
		t.Fatalf("requests=%+v", c.requests)
	}
}

func TestSystemPorts_UnrelatedRevisionDuringWrite(t *testing.T) {
	_, c, intent := mixedPortIntent(t)
	c.beforeWrite = func(c *revisionPortsClient) {
		if len(c.requests) == 1 {
			c.status.Revision++
		}
	}
	result := intent.Execute().(portsApplyResultMsg)
	if result.err != nil || len(c.requests) != 2 || c.status.MixedAddr != "127.0.0.1:19190" {
		t.Fatalf("result=%+v requests=%+v", result, c.requests)
	}
	if c.requests[0].OperationID == c.requests[1].OperationID {
		t.Fatal("retry reused a cached failed operation ID")
	}
}

func TestSystemPorts_ConcurrentTargetChangeRejected(t *testing.T) {
	for _, duringWrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "before execution", true: "during write"}[duringWrite], func(t *testing.T) {
			_, c, intent := mixedPortIntent(t)
			change := func(c *revisionPortsClient) { c.status.MixedAddr = "127.0.0.1:29190"; c.status.Revision++ }
			if duringWrite {
				c.beforeWrite = change
			} else {
				change(c)
			}
			result := intent.Execute().(portsApplyResultMsg)
			var api protocol.APIError
			if !errors.As(result.err, &api) || api.Code != protocol.CodeRevisionConflict || c.status.MixedAddr != "127.0.0.1:29190" {
				t.Fatalf("result=%+v status=%+v", result, c.status)
			}
			if len(c.requests) > 1 {
				t.Fatalf("overwrote concurrent target: %+v", c.requests)
			}
		})
	}
}

func TestSystemPorts_PreservesConcurrentOtherEndpoint(t *testing.T) {
	_, c, intent := mixedPortIntent(t)
	c.status.ControllerAddr = "127.0.0.1:19090"
	c.status.Revision++
	result := intent.Execute().(portsApplyResultMsg)
	if result.err != nil || c.status.ControllerAddr != "127.0.0.1:19090" || c.status.MixedAddr != "127.0.0.1:19190" {
		t.Fatalf("result=%+v status=%+v", result, c.status)
	}
}

func TestSystemPorts_ConcurrentEndpointCollisionRejected(t *testing.T) {
	_, c, intent := mixedPortIntent(t)
	c.status.ControllerAddr = "127.0.0.1:19190"
	c.status.Revision++
	result := intent.Execute().(portsApplyResultMsg)
	if result.err == nil || len(c.requests) != 0 {
		t.Fatalf("result=%+v requests=%+v", result, c.requests)
	}
}

func TestSystemPorts_FailureRefreshesCachedEndpoints(t *testing.T) {
	m, c, intent := mixedPortIntent(t)
	c.status.MixedAddr = "127.0.0.1:29190"
	c.status.Revision++
	result := intent.Execute().(portsApplyResultMsg)
	if result.err == nil {
		t.Fatal("expected concurrent endpoint conflict")
	}
	m.Update(result)
	if m.onboarding.MixedAddr != c.status.MixedAddr || m.onboarding.Revision != c.status.Revision {
		t.Fatalf("cached=%+v current=%+v", m.onboarding, c.status)
	}
	// A deliberate new edit uses the refreshed target rather than remaining
	// permanently stuck on the original cached revision and endpoint.
	m.editID = rowMixed
	m.editInput.SetValue("127.0.0.1:19190")
	next := m.confirmPortEdit()().(ui.ActionIntentMsg).Execute().(portsApplyResultMsg)
	if next.err != nil || c.status.MixedAddr != "127.0.0.1:19190" {
		t.Fatalf("result=%+v status=%+v", next, c.status)
	}
}

func TestSystemPorts_NoRetryOnOtherFailures(t *testing.T) {
	for _, failure := range []string{"read", "write", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			m := portsModel(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m.ctx = ctx
			c := &revisionPortsClient{Client: m.client, status: m.onboarding}
			m.client = c
			m.listenFree = func(string) bool { return true }
			m.editID = rowMixed
			m.editInput.SetValue("127.0.0.1:19190")
			intent := m.confirmPortEdit()().(ui.ActionIntentMsg)
			want := errors.New("transport unavailable")
			switch failure {
			case "read":
				c.readErr = want
			case "write":
				c.writeErr = want
			case "cancel":
				cancel()
				want = context.Canceled
			}
			result := intent.Execute().(portsApplyResultMsg)
			if !errors.Is(result.err, want) || len(c.requests) > 1 {
				t.Fatalf("result=%+v requests=%+v", result, c.requests)
			}
			if failure != "write" && len(c.requests) != 0 {
				t.Fatal("wrote despite failed observation")
			}
		})
	}
}

func TestSystemPorts_RevisionRetriesBounded(t *testing.T) {
	_, c, intent := mixedPortIntent(t)
	c.beforeWrite = func(c *revisionPortsClient) { c.status.Revision++ }
	result := intent.Execute().(portsApplyResultMsg)
	var api protocol.APIError
	if !errors.As(result.err, &api) || api.Code != protocol.CodeRevisionConflict || len(c.requests) != 3 {
		t.Fatalf("result=%+v requests=%+v", result, c.requests)
	}
}

func TestSystemPorts_FinalWriteFailureReturnsFreshEndpoints(t *testing.T) {
	for _, failure := range []string{"transport", "revision"} {
		t.Run(failure, func(t *testing.T) {
			_, c, intent := mixedPortIntent(t)
			if failure == "transport" {
				c.writeErr = errors.New("response lost after write")
			}
			c.beforeWrite = func(c *revisionPortsClient) {
				c.status.Revision++
				if failure == "transport" || len(c.requests) == 3 {
					c.status.MixedAddr = "127.0.0.1:29190"
				}
			}
			result := intent.Execute().(portsApplyResultMsg)
			if result.err == nil || result.status.MixedAddr != c.status.MixedAddr || result.status.Revision != c.status.Revision {
				t.Fatalf("result=%+v current=%+v", result, c.status)
			}
			if failure == "transport" && (len(c.requests) != 1 || !errors.Is(result.err, c.writeErr)) {
				t.Fatalf("ambiguous write retried or lost cause: %+v", result)
			}
		})
	}
}

func TestSystemPorts_FailedPostWriteReadPreservesBothErrors(t *testing.T) {
	_, c, intent := mixedPortIntent(t)
	c.writeErr = errors.New("write response lost")
	readErr := errors.New("status unavailable")
	c.beforeWrite = func(c *revisionPortsClient) { c.readErr = readErr }
	result := intent.Execute().(portsApplyResultMsg)
	if !errors.Is(result.err, c.writeErr) || !errors.Is(result.err, readErr) || result.status.MixedAddr != "" || len(c.requests) != 1 {
		t.Fatalf("result=%+v requests=%+v", result, c.requests)
	}
}
