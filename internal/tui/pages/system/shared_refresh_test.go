package system

import (
	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
)

func TestSharedOnboarding_AddressChangesInvalidatePortObservation(t *testing.T) {
	m := New(nil, nil)
	status := protocol.OnboardingStatus{MixedAddr: "127.0.0.1:10001"}
	m.SetOnboarding(status)
	if !m.portProbeNeeded {
		t.Fatal("first address snapshot did not request a port observation")
	}
	m.portProbeNeeded = false
	status.Revision++
	m.SetOnboarding(status)
	if m.portProbeNeeded {
		t.Fatal("unrelated revision caused a native port probe")
	}
	status.MixedAddr = "127.0.0.1:10002"
	m.SetOnboarding(status)
	if !m.portProbeNeeded {
		t.Fatal("changed address retained old port observation")
	}
}
func TestSharedOnboarding_ExplicitPageRefreshKeepsPortProbeTrigger(t *testing.T) {
	m := New(nil, nil)
	m.SetSnapshotRefresh(func(bool) tea.Cmd { return nil })
	m.SetOnboarding(protocol.OnboardingStatus{MixedAddr: "127.0.0.1:10001"})
	m.portProbeNeeded = false
	previous := m.portProbeGen
	m.Load()
	if m.portProbeGen <= previous {
		t.Fatal("entry refresh lost its independent port probe")
	}
}
