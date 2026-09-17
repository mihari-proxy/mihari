package system

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"strings"
	"testing"
)

type coreCheckingClient struct {
	fakeClient
	checks int
}

func (c *coreCheckingClient) CheckCoreVersion(context.Context) (protocol.VersionCheck, error) {
	c.checks++
	return protocol.VersionCheck{Latest: "v1.20.0", Channel: "stable"}, nil
}

func TestCoreCheck_InFlightDeduplicationAndStaleChannelResult(t *testing.T) {
	c := &coreCheckingClient{}
	m := New(c, nil)
	m.SetSelfUpdateChannel(func(context.Context) (string, error) { return "main", nil })
	m.SetMutationsEnabled(true)
	m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityCore}}, protocol.CoreStatus{Channel: "stable"})
	first := m.checkCoreVersion()
	if m.checkCoreVersion() != nil {
		t.Fatal("duplicate in-flight request")
	}
	if m.coreUpdateValue() != "Checking…" {
		t.Fatal("missing pending state")
	}
	// A root snapshot can arrive before the old request finishes.
	m.SetSnapshot(m.status, protocol.CoreStatus{Channel: "alpha"})
	_, next := m.Update(first().(ui.PageResultMsg).Result)
	if next == nil || m.coreVersion.channel != "alpha" || !m.coreVersion.checking {
		t.Fatal("old channel result replaced new state")
	}
	m.Update(coreVersionMsg{generation: m.coreVersion.generation - 1, result: protocol.VersionCheck{Latest: "v9.0.0", Channel: "stable"}})
	if !m.coreVersion.checking || m.coreVersion.latest != "" {
		t.Fatal("stale generation accepted")
	}
	m.Update(coreVersionMsg{generation: m.coreVersion.generation, err: errors.New("synthetic secret")})
	if m.coreUpdateValue() != "Check failed" {
		t.Fatalf("failure=%s", m.coreUpdateValue())
	}
	if m.checkCoreVersion() == nil {
		t.Fatal("failed check cannot retry")
	}
}

func TestCoreCheck_UsesLocalVersionForStoppedCore(t *testing.T) {
	m := New(&coreCheckingClient{}, nil)
	m.SetSelfUpdateChannel(func(context.Context) (string, error) { return "main", nil })
	m.SetMutationsEnabled(true)
	m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityCore}}, protocol.CoreStatus{LocalVersion: "v1.20.0", Channel: "stable"})
	runCoreCheckCommands(m, m.checkCoreVersion())
	if !strings.Contains(m.coreUpdateValue(), "Up to date") {
		t.Fatalf("value=%s", m.coreUpdateValue())
	}
}
func runCoreCheckCommands(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			runCoreCheckCommands(m, c)
		}
		return
	}
	if routed, ok := msg.(ui.PageResultMsg); ok {
		msg = routed.Result
	}
	_, next := m.Update(msg)
	runCoreCheckCommands(m, next)
}
func TestLoadAutomaticallyChecksCoreVersion(t *testing.T) {
	c := &coreCheckingClient{}
	m := New(c, nil)
	m.SetSelfUpdateChannel(func(context.Context) (string, error) { return "main", nil })
	m.SetMutationsEnabled(true)
	m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityCore}}, protocol.CoreStatus{Version: "v1.19.0", Channel: "stable"})
	runCoreCheckCommands(m, m.Load())
	if c.checks != 1 {
		t.Fatalf("checks=%d", c.checks)
	}
	if !strings.Contains(m.coreUpdateValue(), "v1.20.0") {
		t.Fatalf("missing latest: %s", m.coreUpdateValue())
	}
}
