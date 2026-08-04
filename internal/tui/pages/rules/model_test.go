package rules

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestRules_FilterPreservesEvaluationOrder(t *testing.T) {
	model := New(nil, nil)
	model.SetRules(protocol.RuleList{Rules: []protocol.Rule{
		{Type: "DOMAIN-SUFFIX", Payload: "ignored.test", Proxy: "DIRECT"},
		{Type: "DOMAIN", Payload: "one.test", Proxy: "Proxy"},
		{Type: "IP-CIDR", Payload: "1.1.1.0/24", Proxy: "Proxy"},
		{Type: "DOMAIN", Payload: "two.test", Proxy: "DIRECT"},
	}})
	model.SetFilter("DOMAIN", "", "")
	if got := model.VisibleIndexes(); !reflect.DeepEqual(got, []int{0, 1, 3}) {
		t.Fatalf("indexes=%v", got)
	}
	model.SetFilter("", "DOMAIN", "Proxy")
	if got := model.VisibleIndexes(); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("filtered indexes=%v", got)
	}
}

func TestRules_HasNoSortActionAndReloadsWithR(t *testing.T) {
	client := &fakeClient{rules: protocol.RuleList{Rules: []protocol.Rule{{Type: "MATCH", Proxy: "DIRECT"}}}}
	model := New(client, nil)
	model.SetRules(protocol.RuleList{Rules: []protocol.Rule{{Type: "DOMAIN", Payload: "example.test", Proxy: "Proxy"}}})
	model.FocusFirst()
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: 's', Text: "s"}} {
		model.Update(key)
	}
	if !reflect.DeepEqual(model.VisibleIndexes(), []int{0}) {
		t.Fatalf("rule order changed: %v", model.VisibleIndexes())
	}
	_, command := model.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if command == nil {
		t.Fatal("r did not reload rules")
	}
	model.Update(command())
	if got := model.VisibleIndexes(); !reflect.DeepEqual(got, []int{0}) || model.rules[0].Type != "MATCH" {
		t.Fatalf("rules=%#v indexes=%v", model.rules, got)
	}
}

func TestRules_ControlRowActivatesTabsSearchAndFilters(t *testing.T) {
	model := New(nil, nil)
	model.SetRules(protocol.RuleList{Rules: []protocol.Rule{
		{Type: "DOMAIN", Payload: "one.test", Proxy: "Proxy"},
		{Type: "IP-CIDR", Payload: "1.1.1.0/24", Proxy: "DIRECT"},
	}})
	model.FocusFirst()
	model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !model.searching || command == nil {
		t.Fatalf("searching=%v command=%v", model.searching, command != nil)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.typeFilter != "DOMAIN" || !reflect.DeepEqual(model.VisibleIndexes(), []int{0}) {
		t.Fatalf("type=%q indexes=%v", model.typeFilter, model.VisibleIndexes())
	}
	model.controlIndex = 1
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.view != viewProviders {
		t.Fatalf("view=%d", model.view)
	}
}

func TestModel_SearchSupportsPasteMsg(t *testing.T) {
	model := New(nil, nil)
	model.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !model.searching {
		t.Fatal("expected search mode after /")
	}
	model.Update(tea.PasteMsg{Content: "hello\nworld"})
	if model.query != "helloworld" {
		t.Fatalf("query=%q", model.query)
	}
	_, command := model.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	if command == nil {
		t.Fatal("expected clipboard read command")
	}
	model.Update(command())
	_, leave := model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if model.searching {
		t.Fatal("esc should leave search")
	}
	if leave == nil {
		t.Fatal("expected input mode restore command")
	}
	mode, ok := leave().(ui.InputModeMsg)
	if !ok || mode.Mode != ui.InputNavigation {
		t.Fatalf("mode=%#v", mode)
	}
}

func TestProviders_UpdateOneAndConfirmUpdateAll(t *testing.T) {
	client := &fakeClient{providers: protocol.RuleProviderList{Providers: []protocol.RuleProvider{
		{Name: "OpenAI", Type: "HTTP", Status: "Ready"},
		{Name: "GitHub", Type: "File", Status: "Ready"},
	}}}
	model := New(client, func() string { return "provider-op" })
	model.SetProviders(client.providers)
	model.view = viewProviders
	model.focus = pageFocus{kind: focusRow, row: 0}
	_, command := model.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	if command == nil {
		t.Fatal("u did not update focused provider")
	}
	model.Update(command())
	if !reflect.DeepEqual(client.updated, []string{"OpenAI"}) {
		t.Fatalf("updated=%v", client.updated)
	}

	model.query = "OpenAI"
	_, command = model.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if command == nil {
		t.Fatal("ctrl+u did not request confirmation")
	}
	confirmation, ok := command().(ui.ActionIntentMsg)
	if !ok || confirmation.Execute == nil || confirmation.Action != ui.ActionUpdateAllProviders {
		t.Fatalf("message=%T confirmation=%#v", command(), confirmation)
	}
	if len(model.pending) != 0 {
		t.Fatalf("providers became pending before confirmation: %v", model.pending)
	}
	model.Update(confirmation.Execute())
	if !reflect.DeepEqual(client.updated, []string{"OpenAI", "OpenAI", "GitHub"}) {
		t.Fatalf("updated=%v", client.updated)
	}
}

func TestProviders_DetailsContainOnlySafeFields(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(100, 28)
	model.SetProviders(protocol.RuleProviderList{Providers: []protocol.RuleProvider{{
		Name: "OpenAI", Type: "HTTP", Behavior: "Classical", Format: "YamlRule", RuleCount: 12,
		UpdatedAt: time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC), Status: "Ready",
	}}})
	model.view = viewProviders
	model.focus = pageFocus{kind: focusRow, row: 0}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view := model.View()
	for _, want := range []string{"OpenAI", "HTTP", "Classical", "YamlRule", "12", "Ready"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q: %s", want, view)
		}
	}
	for _, forbidden := range []string{"https://", "token=", "controller", "secret"} {
		if strings.Contains(strings.ToLower(view), forbidden) {
			t.Fatalf("view leaked %q: %s", forbidden, view)
		}
	}
}

func TestProviders_PinsFocusByNameAcrossReload(t *testing.T) {
	model := New(nil, nil)
	model.view = viewProviders
	model.SetProviders(protocol.RuleProviderList{Providers: []protocol.RuleProvider{{Name: "Alpha"}, {Name: "Beta"}}})
	model.focus = pageFocus{kind: focusRow, row: 1}
	model.rememberFocusedProvider()
	model.SetProviders(protocol.RuleProviderList{Providers: []protocol.RuleProvider{{Name: "Beta"}, {Name: "Alpha"}}})
	indexes := model.visibleProviderIndexes()
	if model.focus.row >= len(indexes) || model.providers[indexes[model.focus.row]].Name != "Beta" {
		t.Fatalf("focus=%#v providers=%#v", model.focus, model.providers)
	}
}

type fakeClient struct {
	rules     protocol.RuleList
	providers protocol.RuleProviderList
	updated   []string
}

func (f *fakeClient) Rules(context.Context) (protocol.RuleList, error) { return f.rules, nil }

func (f *fakeClient) RuleProviders(context.Context) (protocol.RuleProviderList, error) {
	return f.providers, nil
}

func (f *fakeClient) UpdateRuleProvider(_ context.Context, name string, _ protocol.MutationRequest) (protocol.MutationResult, error) {
	f.updated = append(f.updated, name)
	return protocol.MutationResult{Schema: "mihari/v1"}, nil
}
