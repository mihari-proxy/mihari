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
	// Down focuses SearchBar and enters character-input mode (no Enter needed).
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if model.focus.kind != focusSearch {
		t.Fatalf("focus=%#v", model.focus)
	}
	if !model.searching || command == nil {
		t.Fatalf("searching=%v command=%v", model.searching, command != nil)
	}
	// Typing filters immediately without a separate enter-to-edit step.
	model.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	if model.query != "o" {
		t.Fatalf("query=%q", model.query)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if model.searching {
		t.Fatal("esc should leave search input")
	}
	// Control strip: Type filter is controlIndex 2 (tabs 0/1, type 2, target 3).
	model.focus = pageFocus{kind: focusControl}
	model.controlIndex = 2
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

func TestRules_SearchNotInControlStrip(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(100, 24)
	model.SetRules(protocol.RuleList{Rules: []protocol.Rule{
		{Type: "DOMAIN", Payload: "one.test", Proxy: "Proxy"},
	}})
	view := model.View()
	control := rulesSectionBodyLine(view, 0)
	if strings.Contains(control, "Search") {
		t.Fatalf("control strip must not embed search: %q", control)
	}
	for _, want := range []string{"Rules", "Providers", "Type", "Target"} {
		if !strings.Contains(control, want) {
			t.Fatalf("control missing %q: %q", want, control)
		}
	}
	search := rulesSectionBodyLine(view, 1)
	if !strings.Contains(search, ui.SearchPlaceholder) && !strings.Contains(search, "/ ") {
		t.Fatalf("search bar missing: %q", search)
	}
}

func TestRules_SearchUsesVisibleColumnsOnly(t *testing.T) {
	model := New(nil, nil)
	model.SetRules(protocol.RuleList{Rules: []protocol.Rule{
		{Type: "DOMAIN", Payload: "match.payload", Proxy: "Proxy"},
		{Type: "IP-CIDR", Payload: "1.1.1.0/24", Proxy: "DIRECT"},
	}})
	model.query = "match.payload"
	if got := model.VisibleIndexes(); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("payload match indexes=%v", got)
	}
	model.query = "DOMAIN"
	if got := model.VisibleIndexes(); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("type match indexes=%v", got)
	}
	model.query = "DIRECT"
	if got := model.VisibleIndexes(); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("target match indexes=%v", got)
	}
	// Providers: match visible columns only.
	model.view = viewProviders
	model.SetProviders(protocol.RuleProviderList{Providers: []protocol.RuleProvider{
		{Name: "OpenAI", Type: "HTTP", Behavior: "Classical", Format: "YamlRule", Status: "Ready", RuleCount: 12},
		{Name: "GitHub", Type: "File", Behavior: "Domain", Format: "MrsRule", Status: "Idle", RuleCount: 3},
	}})
	model.query = "Classical"
	if got := model.visibleProviderIndexes(); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("behavior match indexes=%v", got)
	}
	model.query = "nope"
	if got := model.visibleProviderIndexes(); len(got) != 0 {
		t.Fatalf("unexpected match indexes=%v", got)
	}
}

func TestView_ControlStripHighlightsActiveWhenContentFocused(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(100, 16)
	model.FocusFirst()
	model.controlIndex = 2 // Type filter

	model.SetContentFocused(false)
	railControl := rulesSectionBodyLine(model.View(), 0)
	model.SetContentFocused(true)
	control := rulesSectionBodyLine(model.View(), 0)
	if !strings.Contains(control, "\x1b[") {
		t.Fatalf("active control chip should highlight when content focused: %q", control)
	}
	if control == railControl {
		t.Fatal("content-focused control strip should differ from rail-focused")
	}
}

func rulesSectionBodyLine(view string, n int) string {
	lines := strings.Split(view, "\n")
	body := 0
	for i := 1; i < len(lines); i++ {
		plain := rulesStripANSI(lines[i])
		if strings.HasPrefix(strings.TrimLeft(plain, " "), "╰") {
			break
		}
		if body == n {
			return lines[i]
		}
		body++
	}
	return ""
}

func rulesStripANSI(value string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range value {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && ((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')):
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestView_RuleDataColorsWhileRailFocused(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(100, 16)
	model.SetRules(protocol.RuleList{Rules: []protocol.Rule{
		{Type: "DOMAIN", Payload: "one.test", Proxy: "REJECT"},
	}})
	model.SetContentFocused(false)
	view := model.View()
	typeStyled := ui.StyleRuleType(model.theme, "DOMAIN")
	targetStyled := ui.StyleProxyTarget(model.theme, "REJECT")
	if !strings.Contains(view, typeStyled) {
		t.Fatalf("rule type semantic color missing while rail-focused:\n%s", view)
	}
	if !strings.Contains(view, targetStyled) {
		t.Fatalf("rule target semantic color missing while rail-focused:\n%s", view)
	}
}

func TestView_FocusedRowHighlightOnlyWhenContentFocused(t *testing.T) {
	model := New(nil, nil)
	model.SetRules(protocol.RuleList{Rules: []protocol.Rule{
		{Type: "DOMAIN", Payload: "one.test", Proxy: "Proxy"},
		{Type: "DOMAIN", Payload: "two.test", Proxy: "DIRECT"},
	}})
	model.focus = pageFocus{kind: focusRow, row: 1}

	findPayload := func() string {
		for _, line := range strings.Split(model.View(), "\n") {
			if strings.Contains(line, "two.test") {
				return line
			}
		}
		return ""
	}

	model.SetContentFocused(false)
	railLine := findPayload()
	if railLine == "" || !strings.Contains(railLine, ui.FocusMarker) {
		t.Fatalf("row marker missing while rail-focused: %q", railLine)
	}
	// Data colors may use ANSI; reverse RowFocus must wait for content focus.
	if strings.Contains(railLine, "\x1b[7m") {
		t.Fatalf("row should not use reverse focus chrome while rail owns focus: %q", railLine)
	}

	model.SetContentFocused(true)
	focused := findPayload()
	if focused == "" || !strings.Contains(focused, ui.FocusMarker) {
		t.Fatalf("focused content row missing marker: %q", focused)
	}
	if focused == railLine {
		t.Fatalf("content focus should add RowFocus styling: rail=%q content=%q", railLine, focused)
	}
}

func TestModel_FooterHintsAreContextual(t *testing.T) {
	model := New(nil, nil)
	if hints := model.FooterHints(); !strings.Contains(hints, "/ search") {
		t.Fatalf("default=%q", hints)
	}
	model.searching = true
	if hints := model.FooterHints(); hints != ui.FooterSearchMode {
		t.Fatalf("search=%q", hints)
	}
	model.searching = false
	model.detail = &detailState{title: "x", body: "y"}
	if hints := model.FooterHints(); hints != ui.FooterDetailMode {
		t.Fatalf("detail=%q", hints)
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

func TestRules_SearchDirectTypeAndCursorKeys(t *testing.T) {
	model := New(nil, nil)
	model.SetRules(protocol.RuleList{Rules: []protocol.Rule{
		{Type: "DOMAIN", Payload: "one.test", Proxy: "Proxy"},
	}})
	model.FocusFirst()
	// Focus search via down — input mode starts immediately.
	model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if !model.searching {
		t.Fatal("expected searching after focusing search bar")
	}
	model.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	model.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if model.query != "ab" {
		t.Fatalf("query=%q", model.query)
	}
	// Left/right move cursor; insert in the middle.
	model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	model.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if model.query != "aXb" {
		t.Fatalf("mid insert query=%q", model.query)
	}
	// Page shortcuts disabled while typing.
	model.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if model.query != "aXrb" {
		t.Fatalf("r should type, not reload shortcut: query=%q", model.query)
	}
	// Up leaves input mode and focuses control strip.
	_, leave := model.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if model.searching || model.focus.kind != focusControl {
		t.Fatalf("searching=%v focus=%#v", model.searching, model.focus)
	}
	if leave == nil {
		t.Fatal("expected input mode restore")
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
