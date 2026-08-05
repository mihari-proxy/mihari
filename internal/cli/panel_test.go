package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

type fakePanelClient struct {
	list        protocol.PanelList
	lastID      string
	lastOp      string
	installed   int
	updated     int
	activated   int
	rolledBack  int
	uninstalled int
	reinstalled int
	opened      int
	openURL     string
}

func (f *fakePanelClient) WebGUI(context.Context) (protocol.WebGUIStatus, error) {
	return protocol.WebGUIStatus{Schema: "mihari/v1", GatewayAddr: "127.0.0.1:9191", GatewayHealth: "healthy"}, nil
}
func (f *fakePanelClient) OpenWebGUI(_ context.Context, panelID string) (protocol.WebGUIOpenResult, error) {
	f.opened++
	f.lastID = panelID
	return protocol.WebGUIOpenResult{Schema: "mihari/v1", OpenURL: f.openURL, Panel: panelID}, nil
}
func (f *fakePanelClient) Panels(context.Context) (protocol.PanelList, error) {
	return f.list, nil
}
func (f *fakePanelClient) InstallPanel(_ context.Context, id string, request protocol.PanelInstallRequest) (protocol.MutationResult, error) {
	f.lastID, f.lastOp, f.installed = id, request.OperationID, f.installed+1
	return protocol.MutationResult{Schema: "mihari/v1", OperationID: request.OperationID, Revision: 2}, nil
}
func (f *fakePanelClient) UpdatePanel(_ context.Context, id string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	f.lastID, f.lastOp, f.updated = id, request.OperationID, f.updated+1
	return protocol.MutationResult{Schema: "mihari/v1", OperationID: request.OperationID, Revision: 3}, nil
}
func (f *fakePanelClient) ActivatePanel(_ context.Context, id string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	f.lastID, f.lastOp, f.activated = id, request.OperationID, f.activated+1
	return protocol.MutationResult{Schema: "mihari/v1", OperationID: request.OperationID, Revision: 4}, nil
}
func (f *fakePanelClient) RollbackPanel(_ context.Context, id string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	f.lastID, f.lastOp, f.rolledBack = id, request.OperationID, f.rolledBack+1
	return protocol.MutationResult{Schema: "mihari/v1", OperationID: request.OperationID, Revision: 5}, nil
}
func (f *fakePanelClient) UninstallPanel(_ context.Context, id string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	f.lastID, f.lastOp, f.uninstalled = id, request.OperationID, f.uninstalled+1
	return protocol.MutationResult{Schema: "mihari/v1", OperationID: request.OperationID, Revision: 6}, nil
}
func (f *fakePanelClient) ReinstallPanel(_ context.Context, id string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	f.lastID, f.lastOp, f.reinstalled = id, request.OperationID, f.reinstalled+1
	return protocol.MutationResult{Schema: "mihari/v1", OperationID: request.OperationID, Revision: 7}, nil
}

func TestPanelListHumanAndJSON(t *testing.T) {
	client := &fakePanelClient{list: protocol.PanelList{
		Schema: "mihari/v1",
		Panels: []protocol.PanelStatus{
			{ID: "zashboard", Name: "Zashboard", Active: true, InstalledBuild: "v1", Health: "active"},
			{ID: "metacubexd", Name: "MetaCubeXD", Health: "missing"},
		},
	}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"panel", "list"}, stdout, stderr, Dependencies{PanelClient: client})
	if exit != ExitOK || stderr.Len() != 0 || !strings.Contains(stdout.String(), "* zashboard") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	stdout.Reset()
	exit = Execute(context.Background(), []string{"panel", "list", "--json"}, stdout, stderr, Dependencies{PanelClient: client})
	if exit != ExitOK || !strings.Contains(stdout.String(), `"id":"zashboard"`) {
		t.Fatalf("json exit=%d stdout=%q", exit, stdout)
	}
	if strings.Contains(strings.ToLower(stdout.String()), "token") || strings.Contains(stdout.String(), "secret") {
		t.Fatalf("list leaked secrets: %s", stdout)
	}
}

func TestPanelMutationsAndRollbackRequiresYes(t *testing.T) {
	client := &fakePanelClient{}
	deps := Dependencies{PanelClient: client, NewOperationID: func() string { return "fixed-op" }}
	for _, args := range [][]string{
		{"panel", "install", "zashboard", "--json"},
		{"panel", "update", "zashboard", "--json"},
		{"panel", "use", "zashboard", "--json"},
		{"panel", "rollback", "zashboard", "--yes", "--json"},
		{"panel", "uninstall", "zashboard", "--yes", "--json"},
		{"panel", "reinstall", "zashboard", "--yes", "--json"},
	} {
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if exit := Execute(context.Background(), args, stdout, stderr, deps); exit != ExitOK || stderr.Len() != 0 {
			t.Fatalf("args=%v exit=%d stdout=%q stderr=%q", args, exit, stdout, stderr)
		}
	}
	if client.installed != 1 || client.updated != 1 || client.activated != 1 || client.rolledBack != 1 ||
		client.uninstalled != 1 || client.reinstalled != 1 || client.lastOp != "fixed-op" {
		t.Fatalf("client=%#v", client)
	}
	exit := Execute(context.Background(), []string{"panel", "rollback", "zashboard", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, deps)
	if exit != ExitUsage || client.rolledBack != 1 {
		t.Fatalf("exit=%d rolledBack=%d", exit, client.rolledBack)
	}
	exit = Execute(context.Background(), []string{"panel", "uninstall", "zashboard", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, deps)
	if exit != ExitUsage || client.uninstalled != 1 {
		t.Fatalf("uninstall without --yes exit=%d uninstalled=%d", exit, client.uninstalled)
	}
	exit = Execute(context.Background(), []string{"panel", "reinstall", "zashboard", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, deps)
	if exit != ExitUsage || client.reinstalled != 1 {
		t.Fatalf("reinstall without --yes exit=%d reinstalled=%d", exit, client.reinstalled)
	}
}

func TestPanelOpenDoesNotPrintToken(t *testing.T) {
	client := &fakePanelClient{openURL: "http://127.0.0.1:9191/__mihari/panels/zashboard/?token=super-secret-web-token"}
	var opened string
	deps := Dependencies{
		PanelClient: client,
		OpenBrowser: func(url string) error { opened = url; return nil },
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"panel", "open"}, stdout, stderr, deps)
	if exit != ExitOK || opened != client.openURL {
		t.Fatalf("exit=%d opened=%q", exit, opened)
	}
	if strings.Contains(stdout.String(), "token") || strings.Contains(stdout.String(), "super-secret") {
		t.Fatalf("human open printed token: %s", stdout)
	}
	stdout.Reset()
	exit = Execute(context.Background(), []string{"panel", "open", "metacubexd", "--json"}, stdout, stderr, deps)
	if exit != ExitOK || !strings.Contains(stdout.String(), `"opened":true`) || client.lastID != "metacubexd" {
		t.Fatalf("json exit=%d stdout=%q lastID=%s", exit, stdout, client.lastID)
	}
	if strings.Contains(stdout.String(), "token") || strings.Contains(stdout.String(), "super-secret") {
		t.Fatalf("json open printed token: %s", stdout)
	}
}
