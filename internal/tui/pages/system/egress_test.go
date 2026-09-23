package system

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type egressTestClient struct {
	fakeClient
	calls int
}

func TestNetworkRows_SelectedEgressUsesWarningWithoutSaved(t *testing.T) {
	for _, test := range []struct {
		name       string
		selection  protocol.EgressSelection
		interfaces []protocol.EgressInterface
		want       string
	}{
		{name: "automatic", selection: protocol.EgressSelection{Mode: "automatic"}, want: "No-Override"},
		{name: "manual", selection: protocol.EgressSelection{Mode: "manual", InterfaceName: "Ethernet"}, interfaces: []protocol.EgressInterface{{Name: "Ethernet", Availability: "available", Selectable: true}}, want: "Ethernet"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := New(&egressTestClient{}, nil)
			m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityEgress}}, protocol.CoreStatus{})
			m.SetEgress(protocol.EgressStatus{Selection: test.selection, Interfaces: test.interfaces})
			rows := m.networkRows()
			row := rows[len(rows)-1]
			if row.id != "egress" || rows[0].id == "egress" {
				t.Fatalf("egress is not the last network row: %+v", rows)
			}
			if strings.Contains(row.value, "Saved") {
				t.Fatalf("main row retained Saved: %q", row.value)
			}
			if want := m.theme.BrightYellow.Render(test.want); !strings.Contains(row.value, want) || strings.Contains(row.value, m.theme.Warning.Render(test.want)) {
				t.Fatalf("selected mode is not bright yellow: value=%q", row.value)
			}
			m.SetSize(90, 32)
			m.openEgressDialog()
			view := m.egressDialogView()
			if strings.Contains(view, "Saved") || strings.Contains(view, "[ Apply ]") || strings.Contains(view, "[ Cancel ]") {
				t.Fatalf("dialog kept the old saved/action chrome: %s", view)
			}
			if !strings.Contains(view, m.theme.BrightYellow.Render("▸")) {
				t.Fatalf("cursor on the instance selection missing yellow marker: %s", view)
			}
			if !strings.Contains(view, "Outbound Interface Override") || !strings.Contains(view, "No-Override") {
				t.Fatalf("dialog kept the old Automatic title: %s", view)
			}
		})
	}
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
			if !strings.Contains(view, "VPN-29") || strings.Contains(view, "[ Apply ]") || strings.Contains(view, "[ Cancel ]") || m.egress.top == 0 {
				t.Fatalf("selection not visible: %s", view)
			}
			if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
				t.Fatalf("dialog %dx%d exceeds %v", lipgloss.Width(view), lipgloss.Height(view), size)
			}
			if c.calls != 0 || m.egress.status.Selection.Mode != "automatic" {
				t.Fatal("browsing applied draft")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			if !m.egress.open || m.egress.pending || c.calls != 0 {
				t.Fatal("tab changed the dialog")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if !m.egress.pending || !m.egress.open {
				t.Fatal("enter did not apply the highlighted interface")
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

func TestEgressDialog_EnterAppliesAndStaysOpen(t *testing.T) {
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
	cmd := m.updateEgressDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || !m.egress.pending || !m.egress.open {
		t.Fatal("enter did not start apply")
	}
	if again := m.updateEgressDialog(tea.KeyPressMsg{Code: tea.KeyEnter}); again != nil || !m.egress.pending {
		t.Fatal("duplicate submission")
	}
	m.handleEgressResult(egressResultMsg{mutation: true, epoch: m.egress.epoch, err: protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "reload rejected"}})
	if !m.egress.open || m.egress.pending || m.egress.candidate != "Ethernet" || !strings.Contains(m.egress.err, "reload rejected") {
		t.Fatalf("failure lost the dialog: %+v", m.egress)
	}
	applied := status
	applied.Revision = 2
	applied.Selection = protocol.EgressSelection{Mode: "manual", InterfaceName: "Ethernet"}
	applied.State = "applied"
	m.handleEgressResult(egressResultMsg{mutation: true, epoch: m.egress.epoch, status: applied})
	if !m.egress.open || m.egress.candidate != "Ethernet" || m.egress.status.Selection.InterfaceName != "Ethernet" {
		t.Fatalf("success closed the dialog: %+v", m.egress)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if !strings.Contains(m.egressDialogView(), m.theme.BrightYellow.Render("Ethernet")) {
		t.Fatal("instance selection name is not yellow once the cursor leaves")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	oldEpoch := m.egress.epoch
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.egress.open || m.egress.pending || c.calls != 0 || m.egress.epoch == oldEpoch {
		t.Fatal("enter on the instance selection submitted again")
	}
	m.handleEgressResult(egressResultMsg{epoch: oldEpoch, status: protocol.EgressStatus{Revision: 99}})
	if m.egress.status.Revision == 99 {
		t.Fatal("closed dialog accepted old snapshot")
	}
}

func TestEgressDialog_EnterOnCurrentSelectionClosesWithoutApply(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: tea.KeySpace}} {
		t.Run(key.String(), func(t *testing.T) {
			c := &egressTestClient{}
			m := New(c, func() string { return "op" })
			m.SetSize(90, 32)
			m.SetMutationsEnabled(true)
			m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityEgress}}, protocol.CoreStatus{})
			m.SetEgress(protocol.EgressStatus{Selection: protocol.EgressSelection{Mode: "automatic"}, State: "applied"})
			m.openEgressDialog()
			m.Update(key)
			if m.egress.open || m.egress.pending || c.calls != 0 {
				t.Fatal("enter on the current selection applied or stayed open")
			}
		})
	}
}

func TestEgressDialog_UnconfirmedApplicationIsVisible(t *testing.T) {
	m := New(&egressTestClient{}, func() string { return "op" })
	m.SetSize(90, 32)
	m.egress.open = true
	m.egress.status = protocol.EgressStatus{State: "unknown", Selection: protocol.EgressSelection{Mode: "automatic"}}
	view := m.egressDialogView()
	if !strings.Contains(view, "unconfirmed") || strings.Contains(view, "Enter applies") {
		t.Fatal("application uncertainty hidden")
	}
	m.egress.status.State = "saved"
	view = m.egressDialogView()
	if !strings.Contains(view, "next core start") || strings.Contains(view, "Enter applies") {
		t.Fatal("deferred application hidden")
	}
	m.egress.status.State = "applied"
	view = m.egressDialogView()
	if !strings.Contains(view, "Enter applies") || !strings.Contains(view, "Esc closes") {
		t.Fatalf("apply hint missing: %s", view)
	}
}

func TestEgressDialog_WideRuleSpansBothColumns(t *testing.T) {
	m := New(&egressTestClient{}, func() string { return "op" })
	m.SetSize(90, 32)
	m.egress.open = true
	m.egress.status = protocol.EgressStatus{
		State:     "applied",
		Selection: protocol.EgressSelection{Mode: "automatic"},
		Interfaces: []protocol.EgressInterface{
			{Name: "Ethernet", Availability: "available", Selectable: true},
			{Name: "Wi-Fi", Availability: "available", Selectable: true},
			{Name: "VPN", Availability: "disconnected", Selectable: true},
		},
	}
	rules := 0
	for _, line := range strings.Split(m.egressDialogView(), "\n") {
		if strings.Contains(line, " │ ") {
			rules++
		}
	}
	if rules < 5 {
		t.Fatalf("vertical rule spans %d lines, want the full column height", rules)
	}
}

func TestEgressBoxWidth_UsesSubscriptionOffset(t *testing.T) {
	m := New(&egressTestClient{}, func() string { return "op" })
	m.SetSize(150, 40)
	if m.egressBoxWidth() != 100 {
		t.Fatalf("wide box=%d", m.egressBoxWidth())
	}
	m.SetSize(72, 36)
	if m.egressBoxWidth() != 64 {
		t.Fatalf("medium box=%d", m.egressBoxWidth())
	}
	m.SetSize(40, 24)
	if m.egressBoxWidth() != 40 {
		t.Fatalf("narrow box=%d", m.egressBoxWidth())
	}
}

func TestEgressDialog_UsesAvailableWidthAndWrapsNames(t *testing.T) {
	long := "vEthernet (WSL (Hyper-V firewall))"
	for _, test := range []struct {
		size     [2]int
		minWidth int
		maxWidth int
		wantWrap bool
	}{
		{size: [2]int{150, 40}, minWidth: 100, maxWidth: 100},
		{size: [2]int{72, 36}, minWidth: 64, maxWidth: 64, wantWrap: true},
	} {
		t.Run(fmt.Sprint(test.size), func(t *testing.T) {
			m := New(&egressTestClient{}, func() string { return "op" })
			m.SetSize(test.size[0], test.size[1])
			m.egress.open = true
			m.egress.status = protocol.EgressStatus{
				State:     "applied",
				Selection: protocol.EgressSelection{Mode: "manual", InterfaceName: long},
				Interfaces: []protocol.EgressInterface{
					{Name: long, Availability: "available", Selectable: true, Kind: "unknown", Addresses: []string{"172.19.240.1/20"}},
					{Name: "VMware Network Adapter VMnet1", Availability: "available", Selectable: true},
				},
			}
			view := m.egressDialogView()
			if w, h := lipgloss.Width(view), lipgloss.Height(view); w < test.minWidth || w > test.maxWidth || h > test.size[1] {
				t.Fatalf("dialog %dx%d outside %d-%d of %v", w, h, test.minWidth, test.maxWidth, test.size)
			}
			plain := ansi.Strip(view)
			if strings.Contains(plain, "…") {
				t.Fatalf("name ellipsized:\n%s", plain)
			}
			for _, word := range []string{"vEthernet", "(Hyper-V", "firewall)", "VMnet1"} {
				if !strings.Contains(plain, word) {
					t.Fatalf("missing %s:\n%s", word, plain)
				}
			}
			wrapped := false
			for _, line := range strings.Split(plain, "\n") {
				if strings.Contains(line, "vEthernet") && !strings.Contains(line, "firewall)") {
					wrapped = true
				}
			}
			if wrapped != test.wantWrap {
				t.Fatalf("wrapped=%v:\n%s", wrapped, plain)
			}
		})
	}
}

func TestEgressDialog_ShowsDistinctDeviceInDetails(t *testing.T) {
	m := New(&egressTestClient{}, func() string { return "op" })
	m.SetSize(160, 36)
	m.egress.open = true
	m.egress.candidate = "以太网"
	m.egress.status = protocol.EgressStatus{
		State:     "applied",
		Selection: protocol.EgressSelection{Mode: "manual", InterfaceName: "以太网"},
		Interfaces: []protocol.EgressInterface{
			{Name: "以太网", Device: "Realtek Gaming 2.5GbE Family Controller", Availability: "available", Selectable: true, Kind: "physical"},
			{Name: "VMnet1", Availability: "available", Selectable: true},
		},
	}
	view := m.egressDialogView()
	plain := ansi.Strip(view)
	if !strings.Contains(view, m.theme.Muted.Render("Device")) || !strings.Contains(plain, "Realtek Gaming 2.5GbE Family") || !strings.Contains(plain, "Controller") {
		t.Fatalf("device missing:\n%s", plain)
	}
	m.egress.candidate = "VMnet1"
	if strings.Contains(m.egressDialogView(), "Device") {
		t.Fatal("device row shown for an adapter without a distinct device name")
	}
}

func TestEgressDialog_ListUsesStatusColorAndDetailIndent(t *testing.T) {
	m := New(&egressTestClient{}, func() string { return "op" })
	m.SetSize(160, 36)
	m.SetMutationsEnabled(true)
	m.egress.open = true
	m.egress.loaded = true
	m.egress.candidate = "Wi-Fi"
	m.egress.status = protocol.EgressStatus{
		State:     "applied",
		Selection: protocol.EgressSelection{Mode: "manual", InterfaceName: "Ethernet"},
		Interfaces: []protocol.EgressInterface{
			{Name: "Ethernet", Kind: "physical", Availability: "available", Selectable: true, Addresses: []string{"192.0.2.10/24"}},
			{Name: "Wi-Fi", Kind: "wireless", Availability: "disconnected", Selectable: true},
			{Name: "Meta", Kind: "tun", Availability: "available", Selectable: false, Reason: "Mihari's own TUN interface"},
			{Name: "Gone", Kind: "unknown", Availability: "not_found", Selectable: false, Reason: "missing"},
		},
	}
	view := m.egressDialogView()
	white := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	for _, absent := range []string{"Not applied", "[ Apply ]", "[ Cancel ]", "· Available", "· Disconnected", "Available\n", "Disconnected\n"} {
		if strings.Contains(view, absent) {
			t.Fatalf("dialog still shows %q: %s", absent, view)
		}
	}
	for _, want := range []string{
		m.theme.Info.Render("●"),
		m.theme.Warning.Render("●"),
		m.theme.Danger.Render("●"),
		m.theme.BrightYellow.Render("Ethernet"),
		m.theme.Muted.Render("Name"),
		m.theme.Muted.Render("Status"),
		m.theme.Warning.Render("Disconnected"),
		white.Render("wireless"),
		"● INTERFACES",
		"● DETAILS",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("dialog missing %q: %s", want, view)
		}
	}
	if !strings.Contains(view, m.theme.BrightYellow.Render("▸")) {
		t.Fatal("instance selection lost the yellow marker while the cursor was elsewhere")
	}
	if hints := m.FooterHints(); !strings.Contains(hints, "Enter use") || strings.Contains(hints, "Tab") {
		t.Fatalf("footer=%q", hints)
	}
	m.egress.candidate = "Meta"
	view = m.egressDialogView()
	if !strings.Contains(view, m.theme.Muted.Render("Reason")) || !strings.Contains(view, white.Render("Mihari's own TUN interface")) {
		t.Fatalf("reason missing from detail: %s", view)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.egress.pending || !m.egress.open {
		t.Fatal("enter on a non-selectable interface changed the dialog")
	}
}
