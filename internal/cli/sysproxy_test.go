package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type fakeSystemProxyClient struct {
	status     protocol.SystemProxyStatus
	enabled    protocol.SystemProxyMutationRequest
	disabled   protocol.SystemProxyMutationRequest
	enableErr  error
	disableErr error
	statusErr  error
}

func (f *fakeSystemProxyClient) SystemProxy(context.Context) (protocol.SystemProxyStatus, error) {
	return f.status, f.statusErr
}

func (f *fakeSystemProxyClient) EnableSystemProxy(_ context.Context, request protocol.SystemProxyMutationRequest) (protocol.SystemProxyStatus, error) {
	f.enabled = request
	if f.enableErr != nil {
		return protocol.SystemProxyStatus{}, f.enableErr
	}
	status := f.status
	status.Desired = true
	status.Observed.Enabled = true
	status.Observed.Owned = true
	status.Observed.Foreign = false
	if status.Target != "" {
		status.Observed.Server = status.Target
	}
	return status, nil
}

func (f *fakeSystemProxyClient) DisableSystemProxy(_ context.Context, request protocol.SystemProxyMutationRequest) (protocol.SystemProxyStatus, error) {
	f.disabled = request
	if f.disableErr != nil {
		return protocol.SystemProxyStatus{}, f.disableErr
	}
	status := f.status
	status.Desired = false
	status.Observed.Enabled = false
	status.Observed.Owned = false
	status.Observed.Foreign = false
	return status, nil
}

func TestSysproxyStatusJSONSchema(t *testing.T) {
	client := &fakeSystemProxyClient{status: protocol.SystemProxyStatus{
		Schema:   "mihari/v1",
		Revision: 12,
		Desired:  true,
		Target:   "127.0.0.1:9190",
		Observed: protocol.SystemProxyObserved{
			Enabled: true,
			Server:  "127.0.0.1:9190",
			Owned:   true,
			Foreign: false,
		},
	}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"sysproxy", "status", "--json"}, stdout, stderr, Dependencies{
		SystemProxyClient: client,
	})
	if exit != ExitOK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	var status protocol.SystemProxyStatus
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("stdout=%q err=%v", stdout.String(), err)
	}
	if status.Schema != "mihari/v1" || status.Revision != 12 || !status.Desired || status.Target != "127.0.0.1:9190" {
		t.Fatalf("status=%#v", status)
	}
	if !status.Observed.Enabled || !status.Observed.Owned || status.Observed.Foreign || status.Observed.Server != "127.0.0.1:9190" {
		t.Fatalf("observed=%#v", status.Observed)
	}
}

func TestSysproxyStatusHumanOutput(t *testing.T) {
	client := &fakeSystemProxyClient{status: protocol.SystemProxyStatus{
		Desired: true,
		Target:  "127.0.0.1:9190",
		Observed: protocol.SystemProxyObserved{
			Enabled: true,
			Server:  "127.0.0.1:9190",
			Owned:   true,
			Foreign: false,
		},
	}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"sysproxy", "status"}, stdout, stderr, Dependencies{
		SystemProxyClient: client,
	})
	if exit != ExitOK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"Desired: true", "Target: 127.0.0.1:9190", "Enabled: true", "Server: 127.0.0.1:9190", "Owned: true", "Foreign: false"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %q", want, out)
		}
	}
}

func TestSysproxyEnableConflictExitCode(t *testing.T) {
	client := &fakeSystemProxyClient{
		enableErr: protocol.APIError{
			Code:    protocol.CodeSystemProxyConflict,
			Message: "system proxy is managed by another application",
			Details: map[string]any{"current_server": "127.0.0.1:7890", "target_server": "127.0.0.1:9190"},
		},
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"sysproxy", "enable", "--json"}, stdout, stderr, Dependencies{
		SystemProxyClient: client,
		NewOperationID:    func() string { return "op-sysproxy" },
	})
	if exit != ExitConflict || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("stderr=%q err=%v", stderr.String(), err)
	}
	if envelope.Error.Code != protocol.CodeSystemProxyConflict {
		t.Fatalf("error=%#v", envelope.Error)
	}
	if client.enabled.OperationID != "op-sysproxy" || client.enabled.Force {
		t.Fatalf("request=%#v", client.enabled)
	}
}

func TestSysproxyEnableForceSuccess(t *testing.T) {
	client := &fakeSystemProxyClient{status: protocol.SystemProxyStatus{
		Schema:   "mihari/v1",
		Revision: 13,
		Target:   "127.0.0.1:9190",
	}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"sysproxy", "enable", "--force", "--json"}, stdout, stderr, Dependencies{
		SystemProxyClient: client,
		NewOperationID:    func() string { return "op-force" },
	})
	if exit != ExitOK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", exit, stderr.String(), stdout.String())
	}
	if !client.enabled.Force || client.enabled.OperationID != "op-force" {
		t.Fatalf("request=%#v", client.enabled)
	}
	var status protocol.SystemProxyStatus
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("stdout=%q err=%v", stdout.String(), err)
	}
	if status.Schema != "mihari/v1" || !status.Desired || !status.Observed.Owned {
		t.Fatalf("status=%#v", status)
	}
}

func TestSysproxyDisableNotOwnedExitCode(t *testing.T) {
	client := &fakeSystemProxyClient{
		disableErr: protocol.APIError{
			Code:    protocol.CodeSystemProxyNotOwned,
			Message: "system proxy is not managed by mihari",
		},
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"sysproxy", "disable", "--json"}, stdout, stderr, Dependencies{
		SystemProxyClient: client,
		NewOperationID:    func() string { return "op-disable" },
	})
	if exit != ExitUsage || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("stderr=%q err=%v", stderr.String(), err)
	}
	if envelope.Error.Code != protocol.CodeSystemProxyNotOwned {
		t.Fatalf("error=%#v", envelope.Error)
	}
	if client.disabled.OperationID != "op-disable" {
		t.Fatalf("request=%#v", client.disabled)
	}
}
