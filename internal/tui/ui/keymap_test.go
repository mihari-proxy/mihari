package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode"
)

func TestRenderHelp_CurrentPageComesBeforeOtherPages(t *testing.T) {
	body := RenderHelp(PageProxies, "")
	thisPage := strings.Index(body, "This page · "+PageLabel(PageProxies))
	subs := strings.Index(body, PageLabel(PageSubscriptions)+":")
	if thisPage < 0 || !strings.Contains(body, "Global:") {
		t.Fatalf("missing Global or current page:\n%s", body)
	}
	if subs < 0 || thisPage > subs {
		t.Fatalf("current page must precede Subscriptions:\n%s", body)
	}
	if !strings.Contains(body, "Ctrl+T") || !strings.Contains(body, "test all") {
		t.Fatalf("proxies keys missing:\n%s", body)
	}
}

func TestRenderHelp_CurrentModeComesAfterGlobal(t *testing.T) {
	body := RenderHelp(PageConnections, ModeSearch)
	global := strings.Index(body, "Global:")
	mode := strings.Index(body, "This mode · Search")
	page := strings.Index(body, "This page · "+PageLabel(PageConnections))
	if global < 0 || mode < 0 || page < 0 || !(global < mode && mode < page) {
		t.Fatalf("order Global < Search < Connections:\n%s", body)
	}
}

func TestRenderHelp_SameKeyKeepsPageSpecificActions(t *testing.T) {
	body := RenderHelp(PageConnections, "")
	conn := helpSection(t, body, "This page · "+PageLabel(PageConnections)+":")
	subs := helpSection(t, body, PageLabel(PageSubscriptions)+":")
	rules := helpSection(t, body, PageLabel(PageRules)+":")
	web := helpSection(t, body, PageLabel(PageWebGUI)+":")
	if !strings.Contains(conn, "p") || !strings.Contains(conn, "pause") {
		t.Fatalf("connections missing p/pause:\n%s", conn)
	}
	if strings.Contains(conn, "cycle proxy") {
		t.Fatalf("subscriptions action leaked into connections:\n%s", conn)
	}
	if !strings.Contains(subs, "p") || !strings.Contains(subs, "cycle proxy") {
		t.Fatalf("subscriptions missing p/cycle proxy:\n%s", subs)
	}
	if !strings.Contains(subs, "u") || !strings.Contains(subs, "activate") {
		t.Fatalf("subscriptions missing u/activate:\n%s", subs)
	}
	if strings.Contains(subs, "update the focused provider") {
		t.Fatalf("rules action leaked into subscriptions:\n%s", subs)
	}
	if !strings.Contains(rules, "u") || !strings.Contains(rules, "update the focused provider") {
		t.Fatalf("rules missing u/update provider:\n%s", rules)
	}
	if !strings.Contains(web, "u") || !strings.Contains(strings.ToLower(web), "update") {
		t.Fatalf("web gui missing u/update:\n%s", web)
	}
	if strings.Contains(web, "activate") {
		t.Fatalf("subscriptions activate leaked into web gui:\n%s", web)
	}
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasSuffix(trim, ":") {
			continue
		}
		if !strings.Contains(line, "  ") {
			t.Fatalf("help row is not key+action: %q", line)
		}
	}
}

func helpSection(t *testing.T, body, header string) string {
	t.Helper()
	start := strings.Index(body, header)
	if start < 0 {
		t.Fatalf("missing section %q in:\n%s", header, body)
	}
	rest := body[start:]
	lines := strings.Split(rest, "\n")
	var b strings.Builder
	b.WriteString(lines[0])
	b.WriteByte('\n')
	for _, line := range lines[1:] {
		if line != "" && !strings.HasPrefix(line, " ") && strings.HasSuffix(line, ":") {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestCatalog_FooterTokensHaveBindings(t *testing.T) {
	cat := Catalog()
	cases := []struct {
		footer string
		want   []string
	}{
		{FooterRail, []string{"↑/↓", "Enter", "?", "q"}},
		{FooterProxies, []string{"Enter", "t", "Ctrl+T"}},
		{FooterConnections, []string{"/", "x", "p", "Enter"}},
		{FooterRules, []string{"/", "r", "u", "Ctrl+U", "Enter"}},
		{FooterLogs, []string{"/", "p", "w", "G", "Enter"}},
		{FooterSubscriptions, []string{"a", "e", "Space", "p", "r", "Ctrl+R", "u", "d", "Enter"}},
		{FooterWebGUIActions, []string{"Space", "o", "i", "u", "r", "x", "b"}},
		{FooterSystem, []string{"Enter"}},
		{FooterSearchMode, []string{"←/→", "↑/↓", "Esc"}},
		{FooterColumnsMode, []string{"Space", "Enter", "Esc"}},
		{FormHelp, []string{"Tab", "Enter", "Esc"}},
		{FooterPortsEdit, []string{"Enter", "Esc"}},
	}
	for _, tc := range cases {
		for _, token := range tc.want {
			if !catalogHasDisplayFragment(cat, token) {
				t.Fatalf("footer %q token %q missing from catalog", tc.footer, token)
			}
		}
	}
}

func catalogHasDisplayFragment(cat []KeyBinding, token string) bool {
	for _, b := range cat {
		if b.Display == token || strings.Contains(b.Display, token) || strings.Contains(b.Footer, token) {
			return true
		}
	}
	return false
}

func TestCatalog_KeysAppearInHandlerSource(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	uiDir := filepath.Dir(thisFile)
	tuiDir := filepath.Dir(uiDir)
	filesFor := func(b KeyBinding) []string {
		switch {
		case b.Scope == ScopeGlobal:
			return []string{
				filepath.Join(tuiDir, "model.go"),
				filepath.Join(tuiDir, "modal.go"),
			}
		case b.Mode == ModeConfirm:
			return []string{filepath.Join(tuiDir, "modal.go")}
		case b.Mode == ModeSetup || b.Page == PageSetup:
			return []string{
				filepath.Join(tuiDir, "pages", "setup", "model.go"),
				filepath.Join(tuiDir, "model.go"),
			}
		case b.Page == PageOverview:
			return []string{filepath.Join(tuiDir, "pages", "overview", "model.go")}
		case b.Page == PageProxies:
			return []string{filepath.Join(tuiDir, "pages", "proxies", "model.go")}
		case b.Page == PageConnections && b.Mode == ModeDetail:
			return []string{filepath.Join(tuiDir, "pages", "connections", "detail.go")}
		case b.Page == PageConnections:
			return []string{filepath.Join(tuiDir, "pages", "connections", "model.go")}
		case b.Page == PageRules:
			return []string{filepath.Join(tuiDir, "pages", "rules", "model.go")}
		case b.Page == PageLogs:
			return []string{filepath.Join(tuiDir, "pages", "logs", "model.go")}
		case b.Page == PageSubscriptions && b.Mode == ModeForm:
			return []string{
				filepath.Join(tuiDir, "pages", "subscriptions", "form.go"),
				filepath.Join(tuiDir, "pages", "subscriptions", "model.go"),
			}
		case b.Page == PageSubscriptions:
			return []string{filepath.Join(tuiDir, "pages", "subscriptions", "model.go")}
		case b.Page == PageWebGUI:
			return []string{filepath.Join(tuiDir, "pages", "webgui", "model.go")}
		case b.Page == PageSystem:
			return []string{filepath.Join(tuiDir, "pages", "system", "model.go")}
		case b.Mode == ModeSearch:
			return []string{
				filepath.Join(tuiDir, "pages", "connections", "model.go"),
				filepath.Join(tuiDir, "pages", "rules", "model.go"),
				filepath.Join(tuiDir, "pages", "logs", "model.go"),
				filepath.Join(uiDir, "textfield.go"),
			}
		case b.Mode == ModeDetail:
			return []string{
				filepath.Join(tuiDir, "pages", "connections", "detail.go"),
				filepath.Join(tuiDir, "pages", "rules", "model.go"),
				filepath.Join(tuiDir, "pages", "logs", "model.go"),
				filepath.Join(tuiDir, "pages", "subscriptions", "model.go"),
				filepath.Join(tuiDir, "pages", "system", "model.go"),
			}
		case b.Mode == ModeColumns:
			return []string{filepath.Join(tuiDir, "pages", "connections", "model.go")}
		case b.Mode == ModeForm:
			return []string{
				filepath.Join(tuiDir, "pages", "subscriptions", "form.go"),
				filepath.Join(tuiDir, "pages", "subscriptions", "model.go"),
			}
		case b.Mode == ModePortsEdit:
			return []string{filepath.Join(tuiDir, "pages", "system", "model.go")}
		default:
			return nil
		}
	}
	for _, b := range Catalog() {
		for _, key := range b.Keys {
			found := false
			for _, file := range filesFor(b) {
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatalf("read %s: %v", file, err)
				}
				if sourceHasKey(string(data), key) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("key %q (%s) not found in %#v", key, b.Label, filesFor(b))
			}
		}
	}
}

func sourceHasKey(src, key string) bool {
	quoted := `"` + key + `"`
	if strings.Contains(src, "case "+quoted) || strings.Contains(src, ", "+quoted) || strings.Contains(src, "== "+quoted) {
		return true
	}
	if len(key) == 1 && unicode.IsDigit(rune(key[0])) && strings.Contains(src, "func railDigit") {
		return true
	}
	return false
}

func TestRenderHelp_IncludesGlobalJumpAndQuit(t *testing.T) {
	body := RenderHelp(PageOverview, "")
	for _, want := range []string{"1–8", "Ctrl+C", "q", "?"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q:\n%s", want, body)
		}
	}
}

func TestRenderFooter_MatchesCurrentLayout(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"rail", RenderRailFooter(), "↑/↓ page  Enter open  ? help  q quit"},
		{"overview", RenderFooter(PageOverview, "", FooterOpt{}), "Esc back  ? help  q quit"},
		{"proxies", RenderFooter(PageProxies, "", FooterOpt{}), "Esc back  Enter expand  t test  Ctrl+T test all  ? help  q quit"},
		{"connections", RenderFooter(PageConnections, "", FooterOpt{}), "Esc back  / search  x close  p pause  Enter details  ? help  q quit"},
		{"rules", RenderFooter(PageRules, "", FooterOpt{}), "Esc back  / search  r reload  u update  Ctrl+U update all  Enter details  ? help  q quit"},
		{"logs", RenderFooter(PageLogs, "", FooterOpt{}), "Esc back  / search  p pause  w wrap  G newest  Enter details  ? help  q quit"},
		{"subscriptions", RenderFooter(PageSubscriptions, "", FooterOpt{}), "Esc back  Enter details  a add  e edit  Space toggle  p proxy  r refresh  Ctrl+R refresh all  u use  d delete  ? help  q quit"},
		{"webgui-off", RenderFooter(PageWebGUI, "", FooterOpt{}), "Esc back  ? help  q quit"},
		{"webgui-on", RenderFooter(PageWebGUI, "", FooterOpt{WebGUIAvailable: true}), "Esc back  ↑/↓ panel  Space set default  o open  i install  u update  r reinstall  x uninstall  b rollback  ? help  q quit"},
		{"system", RenderFooter(PageSystem, "", FooterOpt{}), "Esc back  Enter activate  ? help  q quit"},
		{"search", RenderFooter(PageConnections, ModeSearch, FooterOpt{}), "Type to filter  ←/→ cursor  ↑/↓ leave  Esc done"},
		{"setup", RenderFooter(PageSetup, "", FooterOpt{}), "Tab fields  Enter continue  Esc back  Ctrl+C quit"},
		{"detail", RenderFooter(PageConnections, ModeDetail, FooterOpt{}), "Enter/Esc close  ? help  q quit"},
		{"columns", RenderFooter(PageConnections, ModeColumns, FooterOpt{}), "↑/↓ column  Space toggle  Enter save  Esc cancel  ? help  q quit"},
		{"form", RenderFooter(PageSubscriptions, ModeForm, FooterOpt{}), "Tab/Shift+Tab fields  Enter next/save  Esc cancel"},
		{"ports", RenderFooter(PageSystem, ModePortsEdit, FooterOpt{}), "Type address  Enter apply  Esc cancel  ? help  q quit"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("%s:\n got %q\nwant %q", tc.name, tc.got, tc.want)
		}
	}
	if FooterProxies != "Esc back  Enter expand  t test  Ctrl+T test all  ? help  q quit" {
		t.Fatalf("FooterProxies=%q", FooterProxies)
	}
	if FormHelp != "Tab/Shift+Tab fields  Enter next/save  Esc cancel" {
		t.Fatalf("FormHelp=%q", FormHelp)
	}
	if FooterRail != "↑/↓ page  Enter open  ? help  q quit" {
		t.Fatalf("FooterRail=%q", FooterRail)
	}
	if SetupFooter != "Tab fields  Enter continue  Esc back  Ctrl+C quit" {
		t.Fatalf("SetupFooter=%q", SetupFooter)
	}
}
