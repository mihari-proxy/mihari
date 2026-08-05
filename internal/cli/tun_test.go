package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

type fakeTunClient struct {
	status     protocol.TunStatus
	enabled    protocol.TunMutationRequest
	disabled   protocol.TunMutationRequest
	enableErr  error
	disableErr error
	statusErr  error
}

func (f *fakeTunClient) Tun(context.Context) (protocol.TunStatus, error) {
	return f.status, f.statusErr
}

func (f *fakeTunClient) EnableTun(_ context.Context, request protocol.TunMutationRequest) (protocol.TunStatus, error) {
	f.enabled = request
	if f.enableErr != nil {
		return protocol.TunStatus{}, f.enableErr
	}
	status := f.status
	status.DesiredEnable = true
	status.Managed = true
	live := true
	status.LiveEnable = &live
	if status.Stack == "" {
		status.Stack = "gVisor"
	}
	return status, nil
}

func (f *fakeTunClient) DisableTun(_ context.Context, request protocol.TunMutationRequest) (protocol.TunStatus, error) {
	f.disabled = request
	if f.disableErr != nil {
		return protocol.TunStatus{}, f.disableErr
	}
	status := f.status
	status.DesiredEnable = false
	status.Managed = true
	live := false
	status.LiveEnable = &live
	return status, nil
}

func TestTunStatusJSONSchema(t *testing.T) {
	live := true
	client := &fakeTunClient{status: protocol.TunStatus{
		Schema:        "mihari/v1",
		Revision:      12,
		DesiredEnable: true,
		LiveEnable:    &live,
		Stack:         "gVisor",
		Managed:       true,
	}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"tun", "status", "--json"}, stdout, stderr, Dependencies{
		TunClient: client,
	})
	if exit != ExitOK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	var status protocol.TunStatus
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("stdout=%q err=%v", stdout.String(), err)
	}
	if status.Schema != "mihari/v1" || status.Revision != 12 || !status.DesiredEnable ||
		!status.Managed || status.Stack != "gVisor" || status.LiveEnable == nil || !*status.LiveEnable {
		t.Fatalf("status=%#v", status)
	}
}

func TestTunStatusHumanOutput(t *testing.T) {
	live := true
	client := &fakeTunClient{status: protocol.TunStatus{
		DesiredEnable: true,
		LiveEnable:    &live,
		Stack:         "gVisor",
		Managed:       true,
		Revision:      3,
	}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"tun", "status"}, stdout, stderr, Dependencies{
		TunClient: client,
	})
	if exit != ExitOK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"Desired: true", "Live: true", "Stack: gVisor", "Managed: true", "Revision: 3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %q", want, out)
		}
	}
}

func TestTunStatusHumanLiveUnavailable(t *testing.T) {
	client := &fakeTunClient{status: protocol.TunStatus{
		DesiredEnable: false,
		Managed:       false,
		Revision:      1,
	}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"tun", "status"}, stdout, stderr, Dependencies{
		TunClient: client,
	})
	if exit != ExitOK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Live: -") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestTunEnablePermissionExitCode(t *testing.T) {
	client := &fakeTunClient{
		enableErr: protocol.APIError{
			Code:    protocol.CodePermissionDenied,
			Message: "TUN requires elevated privileges",
		},
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"tun", "enable", "--json"}, stdout, stderr, Dependencies{
		TunClient:      client,
		NewOperationID: func() string { return "op-tun" },
	})
	if exit != ExitPermission || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("stderr=%q err=%v", stderr.String(), err)
	}
	if envelope.Error.Code != protocol.CodePermissionDenied {
		t.Fatalf("error=%#v", envelope.Error)
	}
	if client.enabled.OperationID != "op-tun" {
		t.Fatalf("request=%#v", client.enabled)
	}
}

func TestTunEnableSuccess(t *testing.T) {
	client := &fakeTunClient{status: protocol.TunStatus{
		Schema:   "mihari/v1",
		Revision: 13,
		Stack:    "system",
	}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"tun", "enable", "--if-revision", "12", "--json"}, stdout, stderr, Dependencies{
		TunClient:      client,
		NewOperationID: func() string { return "op-enable" },
	})
	if exit != ExitOK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", exit, stderr.String(), stdout.String())
	}
	if client.enabled.OperationID != "op-enable" || client.enabled.IfRevision == nil || *client.enabled.IfRevision != 12 {
		t.Fatalf("request=%#v", client.enabled)
	}
	var status protocol.TunStatus
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("stdout=%q err=%v", stdout.String(), err)
	}
	if status.Schema != "mihari/v1" || !status.DesiredEnable || !status.Managed {
		t.Fatalf("status=%#v", status)
	}
}

func TestTunDisableSuccess(t *testing.T) {
	client := &fakeTunClient{status: protocol.TunStatus{
		Schema:        "mihari/v1",
		Revision:      14,
		DesiredEnable: true,
		Managed:       true,
		Stack:         "gVisor",
	}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"tun", "disable", "--json"}, stdout, stderr, Dependencies{
		TunClient:      client,
		NewOperationID: func() string { return "op-disable" },
	})
	if exit != ExitOK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", exit, stderr.String(), stdout.String())
	}
	if client.disabled.OperationID != "op-disable" {
		t.Fatalf("request=%#v", client.disabled)
	}
	var status protocol.TunStatus
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("stdout=%q err=%v", stdout.String(), err)
	}
	if status.DesiredEnable || !status.Managed {
		t.Fatalf("status=%#v", status)
	}
}
