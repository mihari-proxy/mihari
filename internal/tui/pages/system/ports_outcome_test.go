package system

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestSystemPorts_DonePreservesSavedEndpoint(t *testing.T) {
	for _, tc := range []struct {
		id    string
		label string
		set   func(*protocol.OnboardingStatus, string)
	}{
		{rowMixed, ui.MixedLabel, func(s *protocol.OnboardingStatus, addr string) { s.MixedAddr = addr }},
		{rowController, ui.ControllerPortLabel, func(s *protocol.OnboardingStatus, addr string) { s.ControllerAddr = addr }},
		{rowWeb, ui.WebPortLabel, func(s *protocol.OnboardingStatus, addr string) { s.WebAddr = addr }},
	} {
		t.Run(tc.id, func(t *testing.T) {
			m := portsModel(t)
			m.SetSize(84, 0)
			m.focusID = tc.id
			m.pending = true
			m.pendingRow = tc.id
			m.portHolds[tc.id] = ui.PortHold{Kind: ui.PortHoldOwned}
			status := m.onboarding
			const savedAddr = "127.0.0.1:19192"
			tc.set(&status, savedAddr)
			m.Update(portsApplyResultMsg{status: status})
			for _, line := range strings.Split(ansi.Strip(m.View()), "\n") {
				if !strings.Contains(line, tc.label) {
					continue
				}
				addrIndex := strings.Index(line, savedAddr)
				doneIndex := strings.Index(line, ui.DoneLabel)
				if addrIndex < 0 || doneIndex <= addrIndex || !strings.Contains(line, ui.PortOwned) {
					t.Fatalf("saved endpoint and ownership must precede Done: %q", line)
				}
				return
			}
			t.Fatalf("missing endpoint row %q", tc.label)
		})
	}
}
