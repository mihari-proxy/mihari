package rules

import (
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestView_DualSectionFraming(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(80, 24)
	model.SetRules(protocol.RuleList{Rules: []protocol.Rule{
		{Type: "DOMAIN", Payload: "example.com", Proxy: "DIRECT"},
	}})
	view := model.View()
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╰") {
		t.Fatalf("missing section borders:\n%s", view)
	}
	if !strings.Contains(view, ui.ControlsSectionTitle) {
		t.Fatalf("missing Controls:\n%s", view)
	}
	want := ui.FormatRulesTitle(true, 1)
	if !strings.Contains(view, want) {
		t.Fatalf("missing list title %q:\n%s", want, view)
	}
}

func TestView_ProvidersTitle(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(80, 24)
	model.view = viewProviders
	model.SetProviders(protocol.RuleProviderList{Providers: []protocol.RuleProvider{
		{Name: "geoip", Type: "http", Status: "ok"},
	}})
	view := model.View()
	want := ui.FormatRulesTitle(false, 1)
	if !strings.Contains(view, want) {
		t.Fatalf("missing providers title %q:\n%s", want, view)
	}
}
