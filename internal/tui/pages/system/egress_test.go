package system

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type egressTestClient struct {
	fakeClient
	calls int
}

func TestEgressDialog_ScrollsDraftAndKeepsActionsVisible(t *testing.T) {
	for _, size := range [][2]int{{90, 32}, {60, 26}, {42, 22}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			c := &egressTestClient{}
			m := New(c, func() string { return "op" })
			m.SetSize(size[0], size[1])
			m.SetMutationsEnabled(true)
			m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityEgress}}, protocol.CoreStatus{})
			status := protocol.EgressStatus{Selection: protocol.EgressSelection{Mode: "automatic"}}
			for i := range 30 {
				status.Interfaces = append(status.Interfaces, protocol.EgressInterface{Name: fmt.Sprintf("VPN-%02d", i), Availability: "available", Selectable: true})
			}
			m.SetEgress(status)
			m.openEgressDialog()
			for range 30 {
				m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			view := m.egressDialogView()
			if !strings.Contains(view, "VPN-29") || !strings.Contains(view, "Apply") || m.egress.top == 0 {
				t.Fatalf("selection not visible: %s", view)
			}
			if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
				t.Fatalf("dialog %dx%d exceeds %v", lipgloss.Width(view), lipgloss.Height(view), size)
			}
			if c.calls != 0 || m.egress.status.Selection.Mode != "automatic" {
				t.Fatal("browsing applied draft")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			if m.egress.focus != 1 {
				t.Fatal("Tab did not focus Cancel")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.egress.open || c.calls != 0 {
				t.Fatal("cancel changed setting")
			}
		})
	}
}

func (c *egressTestClient) Egress(context.Context) (protocol.EgressStatus, error) {
	return protocol.EgressStatus{Selection: protocol.EgressSelection{Mode: "automatic"}, Interfaces: []protocol.EgressInterface{{Name: "Ethernet", Selectable: true, Availability: "available"}}}, nil
}
func (c *egressTestClient) UpdateEgress(context.Context, protocol.EgressUpdateRequest) (protocol.EgressStatus, error) {
	c.calls++
	return protocol.EgressStatus{}, nil
}

func TestEgressDialog_EnterOpensWithoutApplying(t *testing.T) {
	c := &egressTestClient{}
	m := New(c, func() string { return "egress-op" })
	m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityEgress}}, protocol.CoreStatus{})
	m.SetMutationsEnabled(true)
	m.SetSize(90, 32)
	m.focusID = "egress"
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.HelpMode() != "egress" {
		t.Fatal("outbound interface dialog did not open")
	}
	if c.calls != 0 {
		t.Fatal("opening applied a selection")
	}
}

func TestEgressDialog_LongDetailsStayWithinWindow(t *testing.T) {
	m := New(&egressTestClient{}, func() string { return "op" })
	m.SetSize(90, 26)
	m.egress.open = true
	m.egress.candidate = strings.Repeat("����", 50)
	m.egress.status.Interfaces = []protocol.EgressInterface{{Name: m.egress.candidate, Selectable: true}}
	for range 30 {
		m.egress.status.Interfaces[0].Addresses = append(m.egress.status.Interfaces[0].Addresses, "2001:db8:1234:5678:1234:5678:abcd:1234/128")
	}
	view := m.egressDialogView()
	if lipgloss.Width(view) > 90 || lipgloss.Height(view) > 26 {
		t.Fatalf("dialog exceeds viewport: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
	}
}
func TestEgressDialog_DisconnectInvalidatesInflightResult(t *testing.T) {
	m := New(&egressTestClient{}, func() string { return "op" })
	m.SetMutationsEnabled(true)
	m.egress.open, m.egress.pending = true, true
	epoch := m.egress.epoch
	m.SetMutationsEnabled(false)
	m.handleEgressResult(egressResultMsg{epoch: epoch, mutation: true, status: protocol.EgressStatus{Revision: 99}})
	if m.egress.status.Revision == 99 || m.egress.pending {
		t.Fatal("late result accepted after disconnect")
	}
}

func TestEgressDialog_ApplyIsExplicitAndFailureKeepsDraft(t *testing.T) {
	c := &egressTestClient{}
	m := New(c, func() string { return "op" })
	m.SetSize(90, 32)
	m.SetMutationsEnabled(true)
	m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityEgress}}, protocol.CoreStatus{})
	status, err := c.Egress(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	m.SetEgress(status)
	m.openEgressDialog()
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.egress.pending {
		t.Fatal("list Enter applied")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	cmd := m.updateEgressDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || !m.egress.pending {
		t.Fatal("explicit Apply did not start")
	}
	if again := m.updateEgressDialog(tea.KeyPressMsg{Code: tea.KeyEnter}); again != nil {
		t.Fatal("duplicate submission")
	}
	m.handleEgressResult(egressResultMsg{mutation: true, epoch: m.egress.epoch, err: protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "reload rejected"}})
	if !m.egress.open || m.egress.pending || m.egress.candidate != "Ethernet" || !strings.Contains(m.egress.err, "reload rejected") {
		t.Fatalf("failure lost draft: %+v", m.egress)
	}
	oldEpoch := m.egress.epoch
	m.closeEgressDialog()
	m.handleEgressResult(egressResultMsg{epoch: oldEpoch, status: protocol.EgressStatus{Revision: 99}})
	if m.egress.status.Revision == 99 {
		t.Fatal("closed dialog accepted old snapshot")
	}
}

func TestEgressDialog_UnconfirmedApplicationIsVisible(t *testing.T) {
	m := New(&egressTestClient{}, func() string { return "op" })
	m.SetSize(90, 32)
	m.egress.status = protocol.EgressStatus{State: "unknown", Selection: protocol.EgressSelection{Mode: "automatic"}}
	if !strings.Contains(m.egressDialogView(), "unconfirmed") {
		t.Fatal("application uncertainty hidden")
	}
}
