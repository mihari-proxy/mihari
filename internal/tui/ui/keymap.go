package ui

import (
	"strings"
	"unicode/utf8"
)

type KeyScope uint8

const (
	ScopeGlobal KeyScope = iota
	ScopePage
	ScopeMode
)

const (
	ModeSearch    = "search"
	ModeDetail    = "detail"
	ModeColumns   = "columns"
	ModeForm      = "form"
	ModePortsEdit = "ports-edit"
	ModeConfirm   = "confirm"
	ModeSetup     = "setup"
)

// KeyBinding is one shortcut in a page or mode. Identity is (Scope, Page, Mode, Keys),
// not the key itself: the same physical key may mean different things on different pages.
type KeyBinding struct {
	Keys    []string
	Display string
	Label   string
	Footer  string // empty = help-only
	Scope   KeyScope
	Page    PageID
	Mode    string
}

// FooterOpt selects state-dependent footer recipes.
type FooterOpt struct {
	WebGUIAvailable bool
}

func Catalog() []KeyBinding {
	return []KeyBinding{
		{Keys: []string{"1", "2", "3", "4", "5", "6", "7", "8"}, Display: "1–8", Label: "jump to a rail page outside text input", Scope: ScopeGlobal},
		{Keys: []string{"?"}, Display: "?", Label: "this help", Footer: "? help", Scope: ScopeGlobal},
		{Keys: []string{"q"}, Display: "q", Label: "quit outside text input", Footer: "q quit", Scope: ScopeGlobal},
		{Keys: []string{"ctrl+c"}, Display: "Ctrl+C", Label: "quit always", Scope: ScopeGlobal},
		{Keys: []string{"up", "down"}, Display: "↑/↓", Label: "select a rail page", Footer: "↑/↓ page", Scope: ScopeGlobal},
		{Keys: []string{"enter"}, Display: "Enter", Label: "open the selected page from the rail", Footer: "Enter open", Scope: ScopeGlobal},
		{Keys: []string{"esc"}, Display: "Esc", Label: "return to the rail, close a dialog, or step back in Setup", Footer: "Esc back", Scope: ScopeGlobal},
		{Keys: []string{"tab"}, Display: "Tab", Label: "move between form fields, dialog buttons, and page controls", Scope: ScopeGlobal},

		{Keys: []string{"esc"}, Display: "Esc", Label: "return to the rail", Scope: ScopePage, Page: PageOverview},

		{Keys: []string{"enter"}, Display: "Enter", Label: "expand a group or select a node", Footer: "Enter expand", Scope: ScopePage, Page: PageProxies},
		{Keys: []string{"t"}, Display: "t", Label: "test the focused node", Footer: "t test", Scope: ScopePage, Page: PageProxies},
		{Keys: []string{"ctrl+t"}, Display: "Ctrl+T", Label: "test all", Footer: "Ctrl+T test all", Scope: ScopePage, Page: PageProxies},
		{Keys: []string{"up", "down", "left", "right"}, Display: "↑/↓/←/→", Label: "move", Scope: ScopePage, Page: PageProxies},

		{Keys: []string{"/"}, Display: "/", Label: "search", Footer: "/ search", Scope: ScopePage, Page: PageConnections},
		{Keys: []string{"x"}, Display: "x", Label: "close the focused connection", Footer: "x close", Scope: ScopePage, Page: PageConnections},
		{Keys: []string{"p"}, Display: "p", Label: "pause or resume", Footer: "p pause", Scope: ScopePage, Page: PageConnections},
		{Keys: []string{"enter"}, Display: "Enter", Label: "open details or activate a control", Footer: "Enter details", Scope: ScopePage, Page: PageConnections},
		{Keys: []string{"ctrl+x"}, Display: "Ctrl+X", Label: "close all active connections", Scope: ScopePage, Page: PageConnections},
		{Keys: []string{"tab"}, Display: "Tab", Label: "move between controls", Scope: ScopePage, Page: PageConnections},
		{Keys: []string{"up", "down", "left", "right"}, Display: "↑/↓/←/→", Label: "move", Scope: ScopePage, Page: PageConnections},

		{Keys: []string{"/"}, Display: "/", Label: "search", Footer: "/ search", Scope: ScopePage, Page: PageRules},
		{Keys: []string{"r"}, Display: "r", Label: "reload", Footer: "r reload", Scope: ScopePage, Page: PageRules},
		{Keys: []string{"u"}, Display: "u", Label: "update the focused provider", Footer: "u update", Scope: ScopePage, Page: PageRules},
		{Keys: []string{"ctrl+u"}, Display: "Ctrl+U", Label: "update all providers", Footer: "Ctrl+U update all", Scope: ScopePage, Page: PageRules},
		{Keys: []string{"enter"}, Display: "Enter", Label: "open details or activate a control", Footer: "Enter details", Scope: ScopePage, Page: PageRules},
		{Keys: []string{"up", "down", "left", "right"}, Display: "↑/↓/←/→", Label: "move", Scope: ScopePage, Page: PageRules},

		{Keys: []string{"/"}, Display: "/", Label: "search", Footer: "/ search", Scope: ScopePage, Page: PageLogs},
		{Keys: []string{"p"}, Display: "p", Label: "pause or resume", Footer: "p pause", Scope: ScopePage, Page: PageLogs},
		{Keys: []string{"w"}, Display: "w", Label: "wrap", Footer: "w wrap", Scope: ScopePage, Page: PageLogs},
		{Keys: []string{"G"}, Display: "G", Label: "jump to newest", Footer: "G newest", Scope: ScopePage, Page: PageLogs},
		{Keys: []string{"enter"}, Display: "Enter", Label: "open details or activate a control", Footer: "Enter details", Scope: ScopePage, Page: PageLogs},
		{Keys: []string{"up", "down", "left", "right"}, Display: "↑/↓/←/→", Label: "move", Scope: ScopePage, Page: PageLogs},

		{Keys: []string{"enter"}, Display: "Enter", Label: "details", Footer: "Enter details", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"a"}, Display: "a", Label: "add", Footer: "a add", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"e"}, Display: "e", Label: "edit", Footer: "e edit", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"space"}, Display: "Space", Label: "enable or disable", Footer: "Space toggle", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"p"}, Display: "p", Label: "cycle proxy mode", Footer: "p proxy", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"r"}, Display: "r", Label: "refresh", Footer: "r refresh", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"ctrl+r"}, Display: "Ctrl+R", Label: "refresh all", Footer: "Ctrl+R refresh all", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"u"}, Display: "u", Label: "activate", Footer: "u use", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"d"}, Display: "d", Label: "delete", Footer: "d delete", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"up", "down"}, Display: "↑/↓", Label: "move", Scope: ScopePage, Page: PageSubscriptions},

		{Keys: []string{"up", "down", "k", "j"}, Display: "↑/↓", Label: "select a panel", Footer: "↑/↓ panel", Scope: ScopePage, Page: PageWebGUI},
		{Keys: []string{"space"}, Display: "Space", Label: "set default", Footer: "Space set default", Scope: ScopePage, Page: PageWebGUI},
		{Keys: []string{"o"}, Display: "o", Label: "open", Footer: "o open", Scope: ScopePage, Page: PageWebGUI},
		{Keys: []string{"i"}, Display: "i", Label: "install", Footer: "i install", Scope: ScopePage, Page: PageWebGUI},
		{Keys: []string{"u"}, Display: "u", Label: "update", Footer: "u update", Scope: ScopePage, Page: PageWebGUI},
		{Keys: []string{"r"}, Display: "r", Label: "reinstall", Footer: "r reinstall", Scope: ScopePage, Page: PageWebGUI},
		{Keys: []string{"x", "d"}, Display: "x / d", Label: "uninstall", Footer: "x uninstall", Scope: ScopePage, Page: PageWebGUI},
		{Keys: []string{"b"}, Display: "b", Label: "rollback", Footer: "b rollback", Scope: ScopePage, Page: PageWebGUI},

		{Keys: []string{"enter"}, Display: "Enter", Label: "activate the focused row", Footer: "Enter activate", Scope: ScopePage, Page: PageSystem},
		{Keys: []string{"up", "down"}, Display: "↑/↓", Label: "move", Scope: ScopePage, Page: PageSystem},

		{Display: "type", Label: "filter the list", Footer: "Type to filter", Scope: ScopeMode, Mode: ModeSearch},
		{Keys: []string{"left", "right"}, Display: "←/→", Label: "move cursor", Footer: "←/→ cursor", Scope: ScopeMode, Mode: ModeSearch},
		{Keys: []string{"up", "down"}, Display: "↑/↓", Label: "leave the field", Footer: "↑/↓ leave", Scope: ScopeMode, Mode: ModeSearch},
		{Keys: []string{"esc"}, Display: "Esc", Label: "finish search", Footer: "Esc done", Scope: ScopeMode, Mode: ModeSearch},

		{Keys: []string{"enter", "esc"}, Display: "Enter / Esc", Label: "close", Footer: "Enter/Esc close", Scope: ScopeMode, Mode: ModeDetail},
		{Keys: []string{"left", "right"}, Display: "←/→", Label: "switch tabs", Scope: ScopePage, Page: PageConnections, Mode: ModeDetail},
		{Keys: []string{"up", "down"}, Display: "↑/↓", Label: "scroll", Scope: ScopePage, Page: PageConnections, Mode: ModeDetail},

		{Keys: []string{"up", "down"}, Display: "↑/↓", Label: "move", Footer: "↑/↓ column", Scope: ScopeMode, Mode: ModeColumns},
		{Keys: []string{"space"}, Display: "Space", Label: "toggle", Footer: "Space toggle", Scope: ScopeMode, Mode: ModeColumns},
		{Keys: []string{"enter"}, Display: "Enter", Label: "save", Footer: "Enter save", Scope: ScopeMode, Mode: ModeColumns},
		{Keys: []string{"esc"}, Display: "Esc", Label: "cancel", Footer: "Esc cancel", Scope: ScopeMode, Mode: ModeColumns},

		{Keys: []string{"tab", "shift+tab"}, Display: "Tab / Shift+Tab", Label: "move between fields", Footer: "Tab/Shift+Tab fields", Scope: ScopeMode, Mode: ModeForm},
		{Keys: []string{"enter"}, Display: "Enter", Label: "next or save", Footer: "Enter next/save", Scope: ScopeMode, Mode: ModeForm},
		{Keys: []string{"esc"}, Display: "Esc", Label: "cancel", Footer: "Esc cancel", Scope: ScopeMode, Mode: ModeForm},

		{Display: "type", Label: "edit the address", Footer: "Type address", Scope: ScopeMode, Mode: ModePortsEdit},
		{Keys: []string{"enter"}, Display: "Enter", Label: "apply", Footer: "Enter apply", Scope: ScopeMode, Mode: ModePortsEdit},
		{Keys: []string{"esc"}, Display: "Esc", Label: "cancel", Footer: "Esc cancel", Scope: ScopeMode, Mode: ModePortsEdit},

		{Keys: []string{"tab", "shift+tab", "left", "right"}, Display: "Tab / ←/→", Label: "toggle Confirm / Cancel", Scope: ScopeMode, Mode: ModeConfirm},
		{Keys: []string{"enter"}, Display: "Enter", Label: "activate the selected button", Scope: ScopeMode, Mode: ModeConfirm},
		{Keys: []string{"esc"}, Display: "Esc", Label: "cancel", Scope: ScopeMode, Mode: ModeConfirm},

		{Keys: []string{"tab", "shift+tab"}, Display: "Tab / Shift+Tab", Label: "move between fields", Footer: "Tab fields", Scope: ScopePage, Page: PageSetup},
		{Keys: []string{"enter"}, Display: "Enter", Label: "continue", Footer: "Enter continue", Scope: ScopePage, Page: PageSetup},
		{Keys: []string{"esc"}, Display: "Esc", Label: "previous step, or cancel on the first step", Footer: "Esc back", Scope: ScopePage, Page: PageSetup},
		{Keys: []string{"ctrl+c"}, Display: "Ctrl+C", Label: "quit always", Footer: "Ctrl+C quit", Scope: ScopePage, Page: PageSetup},
		{Keys: []string{"s"}, Display: "s", Label: "skip GeoIP", Scope: ScopePage, Page: PageSetup},
		{Keys: []string{"q"}, Display: "q", Label: "quit on non-text steps", Scope: ScopePage, Page: PageSetup},
		{Keys: []string{"?"}, Display: "?", Label: "this help on non-text steps", Scope: ScopePage, Page: PageSetup},
	}
}

func footerTokens(keep func(KeyBinding) bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, b := range Catalog() {
		if b.Footer == "" || seen[b.Footer] || !keep(b) {
			continue
		}
		seen[b.Footer] = true
		out = append(out, b.Footer)
	}
	return out
}

func joinFooter(parts []string) string {
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			clean = append(clean, p)
		}
	}
	return strings.Join(clean, "  ")
}

func RenderRailFooter() string {
	tokens := footerTokens(func(b KeyBinding) bool {
		return b.Scope == ScopeGlobal && (b.Footer == "↑/↓ page" || b.Footer == "Enter open")
	})
	return joinFooter(append(tokens, "? help", "q quit"))
}

func RenderFooter(page PageID, mode string, opt FooterOpt) string {
	helpQuit := []string{"? help", "q quit"}
	switch mode {
	case ModeSearch:
		tokens := footerTokens(func(b KeyBinding) bool {
			return b.Mode == mode && (b.Page == "" || b.Page == page)
		})
		return joinFooter(tokens)
	case ModeDetail, ModeColumns, ModePortsEdit:
		tokens := footerTokens(func(b KeyBinding) bool {
			return b.Mode == mode && (b.Page == "" || b.Page == page)
		})
		return joinFooter(append(tokens, helpQuit...))
	case ModeForm:
		return joinFooter(footerTokens(func(b KeyBinding) bool { return b.Mode == ModeForm }))
	default:
		if page == PageSetup {
			return joinFooter(footerTokens(func(b KeyBinding) bool {
				return b.Scope == ScopePage && b.Page == PageSetup && b.Footer != ""
			}))
		}
		if page == PageWebGUI && !opt.WebGUIAvailable {
			return joinFooter(append([]string{"Esc back"}, helpQuit...))
		}
		tokens := footerTokens(func(b KeyBinding) bool {
			return b.Scope == ScopePage && b.Page == page && b.Mode == ""
		})
		return joinFooter(append(append([]string{"Esc back"}, tokens...), helpQuit...))
	}
}

var (
	FooterRail          = RenderRailFooter()
	FooterContent       = RenderFooter(PageOverview, "", FooterOpt{})
	FooterOverview      = FooterContent
	FooterProxies       = RenderFooter(PageProxies, "", FooterOpt{})
	FooterConnections   = RenderFooter(PageConnections, "", FooterOpt{})
	FooterRules         = RenderFooter(PageRules, "", FooterOpt{})
	FooterLogs          = RenderFooter(PageLogs, "", FooterOpt{})
	FooterSubscriptions = RenderFooter(PageSubscriptions, "", FooterOpt{})
	FooterWebGUI        = RenderFooter(PageWebGUI, "", FooterOpt{})
	FooterWebGUIActions = RenderFooter(PageWebGUI, "", FooterOpt{WebGUIAvailable: true})
	FooterSystem        = RenderFooter(PageSystem, "", FooterOpt{})
	FooterSearchMode    = RenderFooter(PageConnections, ModeSearch, FooterOpt{})
	FooterDetailMode    = RenderFooter(PageConnections, ModeDetail, FooterOpt{})
	FooterColumnsMode   = RenderFooter(PageConnections, ModeColumns, FooterOpt{})
	FooterPortsEdit     = RenderFooter(PageSystem, ModePortsEdit, FooterOpt{})
	FormHelp            = RenderFooter(PageSubscriptions, ModeForm, FooterOpt{})
	SetupFooter         = RenderFooter(PageSetup, "", FooterOpt{})
)

func RenderHelp(active PageID, mode string) string {
	cat := Catalog()
	var b strings.Builder
	write := func(title string, rows []KeyBinding) {
		if len(rows) == 0 {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(title)
		b.WriteString(":\n")
		width := 8
		for _, row := range rows {
			if n := utf8.RuneCountInString(row.Display); n > width {
				width = n
			}
		}
		seen := map[string]bool{}
		for _, row := range rows {
			key := row.Display + "\t" + row.Label
			if seen[key] {
				continue
			}
			seen[key] = true
			pad := width - utf8.RuneCountInString(row.Display)
			b.WriteString("  ")
			b.WriteString(row.Display)
			b.WriteString(strings.Repeat(" ", pad+2))
			b.WriteString(row.Label)
			b.WriteString("\n")
		}
	}

	write("Global", filter(cat, func(x KeyBinding) bool { return x.Scope == ScopeGlobal }))

	if mode != "" && mode != ModeSetup {
		write("This mode · "+modeTitle(mode), filter(cat, func(x KeyBinding) bool {
			if x.Mode != mode {
				return false
			}
			return x.Page == "" || x.Page == active
		}))
	}

	write("This page · "+PageLabel(active), filter(cat, func(x KeyBinding) bool {
		return x.Scope == ScopePage && x.Page == active && x.Mode == ""
	}))

	for _, id := range RailPages() {
		if id == active {
			continue
		}
		write(PageLabel(id), filter(cat, func(x KeyBinding) bool {
			return x.Scope == ScopePage && x.Page == id && x.Mode == ""
		}))
	}

	for _, m := range []string{ModeSearch, ModeDetail, ModeColumns, ModeForm, ModePortsEdit, ModeConfirm} {
		if m == mode {
			continue
		}
		write(modeTitle(m), filter(cat, func(x KeyBinding) bool {
			return x.Scope == ScopeMode && x.Mode == m
		}))
	}
	if active != PageSetup {
		write(PageLabel(PageSetup), filter(cat, func(x KeyBinding) bool {
			return x.Page == PageSetup && x.Mode == ""
		}))
	}
	return strings.TrimRight(b.String(), "\n")
}

func modeTitle(mode string) string {
	switch mode {
	case ModeSearch:
		return "Search"
	case ModeDetail:
		return "Detail"
	case ModeColumns:
		return "Columns"
	case ModeForm:
		return "Form"
	case ModePortsEdit:
		return "Ports edit"
	case ModeConfirm:
		return "Confirm"
	case ModeSetup:
		return "Setup"
	default:
		return mode
	}
}

func filter(in []KeyBinding, keep func(KeyBinding) bool) []KeyBinding {
	out := make([]KeyBinding, 0, len(in))
	for _, item := range in {
		if keep(item) {
			out = append(out, item)
		}
	}
	return out
}
