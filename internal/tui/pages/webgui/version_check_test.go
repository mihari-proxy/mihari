package webgui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"strings"
	"testing"
)

type checkingClient struct {
	fakeClient
	checked []string
}

func (c *checkingClient) CheckPanelVersion(_ context.Context, id string) (protocol.VersionCheck, error) {
	c.checked = append(c.checked, id)
	return protocol.VersionCheck{Latest: "v9.0.0"}, nil
}

func TestPanelChecks_IndependentFailureRetryAndSnapshotRefresh(t *testing.T) {
	c := &checkingClient{fakeClient: fakeClient{status: sampleStatus()}}
	m := New(c, []string{protocol.CapabilityWebGUI})
	m.SetStatus(c.status)
	first := m.checkPanelVersions()
	if m.checkPanelVersions() != nil {
		t.Fatal("duplicate in-flight checks")
	}
	if m.latestLabel(c.status.Panels[0]) != "Checking…" {
		t.Fatal("missing pending state")
	}
	commands := first().(tea.BatchMsg)
	// One panel completes while the other is still pending.
	m.Update(commands[0]().(ui.PageResultMsg).Result)
	m.Update(panelVersionMsg{id: "metacubexd", err: errors.New("synthetic secret")})
	m.SetStatus(c.status) // Background status polls have no checked LatestBuild.
	if !strings.Contains(m.latestLabel(c.status.Panels[0]), "v9.0.0") {
		t.Fatal("snapshot erased checked version")
	}
	if m.latestLabel(c.status.Panels[1]) != "Check failed" {
		t.Fatal("failure was not isolated")
	}
	runCheckCommands(m, m.Load())
	if !strings.Contains(m.latestLabel(c.status.Panels[1]), "v9.0.0") {
		t.Fatal("reentry failed to retry")
	}
	panel := c.status.Panels[0]
	panel.InstalledBuild = "v9.0.0"
	if !strings.Contains(m.latestLabel(panel), "Up to date") {
		t.Fatal("installed version not compared")
	}
}
func runCheckCommands(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			runCheckCommands(m, c)
		}
		return
	}
	if routed, ok := msg.(ui.PageResultMsg); ok {
		msg = routed.Result
	}
	_, next := m.Update(msg)
	runCheckCommands(m, next)
}
func TestLoadChecksEveryPanelIncludingUninstalled(t *testing.T) {
	c := &checkingClient{fakeClient: fakeClient{status: sampleStatus()}}
	c.status.Panels[1].InstalledBuild = ""
	for i := range c.status.Panels {
		c.status.Panels[i].LatestBuild = ""
	}
	m := New(c, []string{protocol.CapabilityWebGUI})
	runCheckCommands(m, m.Load())
	if len(c.checked) != 2 {
		t.Fatalf("checked %v; want every panel", c.checked)
	}
	if !strings.Contains(m.View(), "v9.0.0") {
		t.Fatalf("missing latest: %s", m.View())
	}
	if c.installed != 0 || c.updated != 0 {
		t.Fatal("checking installed an update")
	}
}
