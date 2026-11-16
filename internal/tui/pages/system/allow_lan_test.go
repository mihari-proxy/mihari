package system

import (
	tea "charm.land/bubbletea/v2"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"strings"
	"testing"
)

func TestAllowLAN_ToggleConfirmationSavesAndRequestsRestart(t *testing.T) {
	m := portsModel(t)
	c := &revisionPortsClient{Client: m.client, status: m.onboarding}
	m.client = c
	m.focusID = rowAllowLAN
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	intent, ok := cmd().(ui.ActionIntentMsg)
	if !ok || len(c.requests) != 0 || !strings.Contains(intent.Impact, "authentication") {
		t.Fatalf("intent=%+v requests=%v", intent, c.requests)
	}
	result := intent.Execute().(portsApplyResultMsg)
	if result.err != nil || !result.status.AllowLAN || len(c.requests) != 1 || c.requests[0].AllowLAN == nil || !*c.requests[0].AllowLAN || c.requests[0].MixedAddr != nil {
		t.Fatalf("result=%+v requests=%+v", result, c.requests)
	}
	m.focusID = rowMixed // Async completion still belongs to the toggle row.
	m.lastError = "previous invalid endpoint"
	_, cmd = m.Update(result)
	if m.lastError != "" {
		t.Fatalf("successful correction retained error: %s", m.lastError)
	}
	if m.outcomeRow != rowAllowLAN {
		t.Fatalf("outcome row=%s", m.outcomeRow)
	}
	found := false
	for _, command := range cmd().(tea.BatchMsg) {
		if command == nil {
			continue
		}
		if msg, ok := command().(ui.ApplySavedEndpointsMsg); ok {
			found = msg.Status.AllowLAN
		}
	}
	if !found {
		t.Fatal("saved toggle did not request daemon restart")
	}
}

func TestAllowLAN_MixedEditRejectsDisabledLAN(t *testing.T) {
	m := portsModel(t)
	m.editID = rowMixed
	m.editInput.SetValue("0.0.0.0:9190")
	msg := m.confirmPortEdit()()
	if _, ok := msg.(ui.DiagnosticMsg); !ok || !strings.Contains(m.lastError, "Allow LAN") {
		t.Fatalf("msg=%T error=%s", msg, m.lastError)
	}
	if m.editID != rowMixed {
		t.Fatal("invalid edit was discarded")
	}
	m.onboarding.AllowLAN = true
	if _, ok := m.confirmPortEdit()().(ui.ActionIntentMsg); !ok {
		t.Fatal("enabled LAN still rejects wildcard Mixed")
	}
}

func TestAllowLAN_DisableRejectsWildcardWithoutMutation(t *testing.T) {
	m := portsModel(t)
	m.onboarding.AllowLAN, m.onboarding.MixedAddr = true, "0.0.0.0:9190"
	if _, ok := m.confirmAllowLANToggle()().(ui.DiagnosticMsg); !ok || !strings.Contains(m.lastError, "loopback") {
		t.Fatalf("error=%s", m.lastError)
	}
	if !m.onboarding.AllowLAN {
		t.Fatal("rejected disable changed state")
	}
}

func TestAllowLAN_ConcurrentToggleDoesNotOverwrite(t *testing.T) {
	m := portsModel(t)
	c := &revisionPortsClient{Client: m.client, status: m.onboarding}
	m.client = c
	intent := m.confirmAllowLANToggle()().(ui.ActionIntentMsg)
	c.status.AllowLAN = true
	c.status.Revision++
	result := intent.Execute().(portsApplyResultMsg)
	var api protocol.APIError
	if !errors.As(result.err, &api) || api.Code != protocol.CodeRevisionConflict || len(c.requests) != 0 {
		t.Fatalf("result=%+v requests=%v", result, c.requests)
	}
}

func TestAllowLAN_ReadOnlyHasNoMutation(t *testing.T) {
	m := portsModel(t)
	m.SetMutationsEnabled(false)
	if cmd := m.confirmAllowLANToggle(); cmd != nil {
		t.Fatal("disconnected toggle creates mutation")
	}
}

func TestAllowLAN_NetworkRowFollowsOutboundInterfaceWithStatusTone(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			m := portsModel(t)
			m.status.Capabilities = append(m.status.Capabilities, protocol.CapabilityEgress)
			m.onboarding.AllowLAN = enabled
			rows := m.networkRows()
			if len(rows) < 2 || rows[len(rows)-2].id != "egress" || rows[len(rows)-1].id != rowAllowLAN {
				t.Fatalf("Allow LAN must immediately follow outbound interface: %+v", rows)
			}
			want := m.theme.Muted.Render("● Disabled")
			if enabled {
				want = m.theme.Success.Render("● Enabled")
			}
			if got := rows[len(rows)-1].value; got != want {
				t.Fatalf("status=%q want themed dot and label %q", got, want)
			}
		})
	}
}
