package webgui

import (
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"strings"
	"testing"
)

func TestWebGUI_ErrorDisplayKeepsRawCredentialsAndEscapesControls(t *testing.T) {
	model := New(nil, []string{protocol.CapabilityWebGUI})
	model.SetSize(120, 40)
	model.SetStatus(sampleStatus())
	raw := "browser open_url failed: https://fixture.invalid/?token=fixture-original\x1b]52;c;clipboard\a"
	model.Update(mutationDoneMsg{err: errors.New(raw)})
	view := model.View()
	if !strings.Contains(view, diagnostics.EscapeTerminal(raw)) {
		t.Fatal("error text was hidden or controls were not escaped")
	}
	if strings.Contains(view, "\x1b]52;") {
		t.Fatal("error executed a terminal control sequence")
	}
	if !strings.Contains(view, "Zashboard") {
		t.Fatal("error replaced the page with an unavailable placeholder")
	}
}
