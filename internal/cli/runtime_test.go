package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestExecute_NoArgsNonInteractiveRejectsTUI(t *testing.T) {
	exit := Execute(context.Background(), []string{}, io.Discard, io.Discard, Dependencies{})
	if exit != ExitUsage {
		t.Fatalf("exit=%d", exit)
	}
}

func TestExecute_NoArgsInteractiveRunsTUI(t *testing.T) {
	called := false
	exit := Execute(context.Background(), []string{}, io.Discard, io.Discard, Dependencies{
		Interactive: true,
		RunTUI: func(context.Context) error {
			called = true
			return nil
		},
	})
	if exit != ExitOK || !called {
		t.Fatalf("exit=%d called=%v", exit, called)
	}
}

func TestCoreStatusJSON(t *testing.T) {
	client := &fakeRuntimeClient{core: protocol.CoreStatus{Schema: "mihari/v1", Revision: 4, Status: "running", Version: "v1.19.0", PID: 42}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"core", "status", "--json"}, stdout, stderr, Dependencies{RuntimeClient: client})
	if exit != ExitOK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	var status protocol.CoreStatus
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil || status.Version != "v1.19.0" || status.PID != 42 {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestProxySelectUsesInjectedOperationID(t *testing.T) {
	client := &fakeRuntimeClient{}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"proxy", "select", "Auto/Select", "Node A", "--json"}, stdout, stderr, Dependencies{
		RuntimeClient:  client,
		NewOperationID: func() string { return "fixed-operation" },
	})
	if exit != ExitOK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	if client.selectedGroup != "Auto/Select" || client.selected.Name != "Node A" || client.selected.OperationID != "fixed-operation" {
		t.Fatalf("client=%#v", client)
	}
	if stdout.String() != `{"schema":"mihari/v1","operation_id":"fixed-operation"}`+"\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestConnectionsCloseAllRequiresConfirmation(t *testing.T) {
	client := &fakeRuntimeClient{}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"connections", "close-all", "--json"}, stdout, stderr, Dependencies{RuntimeClient: client})
	if exit != ExitUsage || client.closeAllCalls != 0 {
		t.Fatalf("exit=%d calls=%d stderr=%q", exit, client.closeAllCalls, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exit = Execute(context.Background(), []string{"connections", "close-all", "--yes", "--json"}, stdout, stderr, Dependencies{
		RuntimeClient:  client,
		NewOperationID: func() string { return "close-all" },
	})
	if exit != ExitOK || client.closeAllCalls != 1 || stderr.Len() != 0 {
		t.Fatalf("exit=%d calls=%d stderr=%q", exit, client.closeAllCalls, stderr.String())
	}
}

func TestTrafficJSONFollowWritesNDJSON(t *testing.T) {
	client := &fakeRuntimeClient{events: []protocol.StreamEvent{
		{Schema: "mihari/v1", Stream: "traffic", Data: json.RawMessage(`{"up":1,"down":2}`)},
		{Schema: "mihari/v1", Stream: "traffic", Data: json.RawMessage(`{"up":3,"down":4}`)},
	}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"traffic", "--follow", "--json"}, stdout, stderr, Dependencies{RuntimeClient: client})
	if exit != ExitOK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	lines := bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatalf("stdout=%q", stdout.String())
	}
	for _, line := range lines {
		var event protocol.StreamEvent
		if err := json.Unmarshal(line, &event); err != nil || event.Schema != "mihari/v1" || event.Stream != "traffic" {
			t.Fatalf("line=%s event=%#v err=%v", line, event, err)
		}
	}
}

func TestRuntimeQueryCommandsRenderJSON(t *testing.T) {
	client := &fakeRuntimeClient{
		groups:      protocol.ProxyGroups{Schema: "mihari/v1", Groups: []protocol.ProxyGroup{}},
		connections: protocol.ConnectionList{Schema: "mihari/v1", Connections: []protocol.Connection{}},
		rules:       protocol.RuleList{Schema: "mihari/v1", Rules: []protocol.Rule{}},
	}
	for _, args := range [][]string{{"proxy", "groups", "--json"}, {"connections", "list", "--json"}, {"rules", "list", "--json"}} {
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if exit := Execute(context.Background(), args, stdout, stderr, Dependencies{RuntimeClient: client}); exit != ExitOK || stderr.Len() != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"schema":"mihari/v1"`)) {
			t.Fatalf("args=%v exit=%d stdout=%q stderr=%q", args, exit, stdout.String(), stderr.String())
		}
	}
}

type fakeRuntimeClient struct {
	core          protocol.CoreStatus
	groups        protocol.ProxyGroups
	connections   protocol.ConnectionList
	rules         protocol.RuleList
	events        []protocol.StreamEvent
	selectedGroup string
	selected      protocol.ProxySelectionRequest
	closeAllCalls int
}

func (c *fakeRuntimeClient) Core(context.Context) (protocol.CoreStatus, error) { return c.core, nil }

func (c *fakeRuntimeClient) InstallCore(context.Context, protocol.MutationRequest) (protocol.CoreInstallResult, error) {
	return protocol.CoreInstallResult{Schema: "mihari/v1", Version: "v1.19.0", Updated: true}, nil
}

func (c *fakeRuntimeClient) RestartCore(_ context.Context, request protocol.MutationRequest) (protocol.MutationResult, error) {
	return protocol.MutationResult{Schema: "mihari/v1", OperationID: request.OperationID}, nil
}

func (c *fakeRuntimeClient) ProxyGroups(context.Context) (protocol.ProxyGroups, error) {
	return c.groups, nil
}

func (c *fakeRuntimeClient) SelectProxy(_ context.Context, group string, request protocol.ProxySelectionRequest) (protocol.MutationResult, error) {
	c.selectedGroup = group
	c.selected = request
	return protocol.MutationResult{Schema: "mihari/v1", OperationID: request.OperationID}, nil
}

func (c *fakeRuntimeClient) DelayTest(context.Context, string, protocol.DelayTestRequest) (protocol.DelayResult, error) {
	return protocol.DelayResult{Schema: "mihari/v1", Delays: map[string]uint16{"DIRECT": 1}}, nil
}

func (c *fakeRuntimeClient) Connections(context.Context) (protocol.ConnectionList, error) {
	return c.connections, nil
}

func (c *fakeRuntimeClient) CloseConnection(_ context.Context, _ string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	return protocol.MutationResult{Schema: "mihari/v1", OperationID: request.OperationID}, nil
}

func (c *fakeRuntimeClient) CloseAllConnections(_ context.Context, request protocol.MutationRequest) (protocol.MutationResult, error) {
	c.closeAllCalls++
	return protocol.MutationResult{Schema: "mihari/v1", OperationID: request.OperationID}, nil
}

func (c *fakeRuntimeClient) Rules(context.Context) (protocol.RuleList, error) { return c.rules, nil }

func (c *fakeRuntimeClient) Stream(_ context.Context, kind string, receive func(protocol.StreamEvent) error) error {
	for _, event := range c.events {
		if event.Stream == kind {
			if err := receive(event); err != nil {
				return err
			}
		}
	}
	return nil
}
