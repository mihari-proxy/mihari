package rules

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type Client interface {
	Rules(context.Context) (protocol.RuleList, error)
	RuleProviders(context.Context) (protocol.RuleProviderList, error)
	UpdateRuleProvider(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
}

type viewKind uint8

const (
	viewRules viewKind = iota
	viewProviders
)

type focusKind uint8

const (
	focusControl focusKind = iota
	focusSearch
	focusRow
)

type pageFocus struct {
	kind focusKind
	row  int
}

type detailState struct {
	title string
	body  string
}

type Model struct {
	client          Client
	newOperationID  func() string
	rules           []protocol.Rule
	providers       []protocol.RuleProvider
	view            viewKind
	focus           pageFocus
	controlIndex    int
	focusedProvider string
	query           string
	queryCursor     int
	typeFilter      string
	targetFilter    string
	behaviorFilter  string
	statusFilter    string
	searching       bool
	pending         map[string]bool
	revision        uint64
	detail          *detailState
	width           int
	height          int
	theme           ui.Theme
	lastError       string
	contentFocused  bool
}

type rulesResultMsg struct {
	result protocol.RuleList
	err    error
}

type providersResultMsg struct {
	result protocol.RuleProviderList
	err    error
}

type providerUpdateResultMsg struct {
	name     string
	revision uint64
	err      error
}

type providersUpdateAllResultMsg struct {
	revision uint64
	err      error
}

// Err implements the shell's action-outcome contract so bulk provider updates
// are classified Succeeded/Failed in the Recent operations ledger.
func (m providersUpdateAllResultMsg) Err() error { return m.err }

var _ interface{ Err() error } = providersUpdateAllResultMsg{}

func New(client Client, newOperationID func() string) *Model {
	if newOperationID == nil {
		newOperationID = defaultOperationID
	}
	return &Model{
		client: client, newOperationID: newOperationID, pending: make(map[string]bool),
		focus: pageFocus{kind: focusControl}, theme: ui.DefaultTheme(),
	}
}

// SetContentFocused reports whether the root shell has given keyboard focus to this page.
func (m *Model) SetContentFocused(focused bool) { m.contentFocused = focused }

// FooterHints returns contextual shortcuts for the root shell footer.
func (m *Model) FooterHints() string {
	switch {
	case m.detail != nil:
		return ui.FooterDetailMode
	case m.searching:
		return ui.FooterSearchMode
	default:
		return ui.FooterRules
	}
}

func (m *Model) ID() ui.PageID { return ui.PageRules }

func (m *Model) SetSize(width, height int) { m.width, m.height = width, height }

func (m *Model) FocusFirst() { m.focus = pageFocus{kind: focusControl} }

func (m *Model) SetRules(result protocol.RuleList) {
	m.rules = append([]protocol.Rule(nil), result.Rules...)
	m.reconcileFocus()
}

func (m *Model) SetProviders(result protocol.RuleProviderList) {
	m.rememberFocusedProvider()
	m.providers = append([]protocol.RuleProvider(nil), result.Providers...)
	if result.Revision != 0 {
		m.revision = result.Revision
	}
	m.reconcileFocus()
}

func (m *Model) SetFilter(query, typeFilter, targetFilter string) {
	m.query, m.typeFilter, m.targetFilter = query, typeFilter, targetFilter
	m.reconcileFocus()
}

func (m *Model) VisibleIndexes() []int {
	indexes := make([]int, 0, len(m.rules))
	visible := []string{"type", "payload", "target"}
	for index, rule := range m.rules {
		cells := map[string]string{
			"type":    rule.Type,
			"payload": rule.Payload,
			"target":  rule.Proxy,
		}
		if !ui.MatchVisibleColumns(cells, visible, m.query) {
			continue
		}
		if m.typeFilter != "" && !strings.EqualFold(rule.Type, m.typeFilter) {
			continue
		}
		if m.targetFilter != "" && !strings.EqualFold(rule.Proxy, m.targetFilter) {
			continue
		}
		indexes = append(indexes, index)
	}
	return indexes
}

func (m *Model) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	switch typed := message.(type) {
	case rulesResultMsg:
		if typed.err != nil {
			m.lastError = ui.RulesUnavailable
		} else {
			m.lastError = ""
			m.SetRules(typed.result)
		}
		return m, nil
	case providersResultMsg:
		if typed.err != nil {
			m.lastError = ui.RuleProvidersUnavailable
		} else {
			m.lastError = ""
			m.SetProviders(typed.result)
		}
		return m, nil
	case providerUpdateResultMsg:
		delete(m.pending, typed.name)
		if typed.err != nil {
			m.lastError = ui.ProviderUpdateFailed
		} else {
			m.lastError = ""
			m.revision = typed.revision
		}
		return m, m.reloadProviders()
	case providersUpdateAllResultMsg:
		clear(m.pending)
		if typed.err != nil {
			m.lastError = ui.ProviderUpdateFailed
		} else {
			m.lastError = ""
			m.revision = typed.revision
		}
		return m, m.reloadProviders()
	}

	if m.searching {
		return m.updateSearch(message)
	}
	// Search bar focus is a character-input surface: typing starts filter without Enter.
	// Page shortcuts (r, u, /…) are disabled here so printable keys edit the query.
	if m.detail == nil && m.focus.kind == focusSearch {
		return m.updateSearchFocus(message)
	}
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if m.detail != nil {
		if key.String() == "esc" || key.String() == "enter" {
			m.detail = nil
		}
		return m, nil
	}
	switch key.String() {
	case "esc":
		return m, func() tea.Msg { return ui.FocusRailMsg{} }
	case "/":
		return m, m.startSearch()
	case "left":
		if m.focus.kind == focusControl {
			m.controlIndex = max(0, m.controlIndex-1)
		}
		return m, nil
	case "right":
		if m.focus.kind == focusControl {
			m.controlIndex = min(3, m.controlIndex+1)
		}
		return m, nil
	case "up":
		return m, m.moveFocus(-1)
	case "down":
		return m, m.moveFocus(1)
	case "enter":
		if m.focus.kind == focusControl {
			return m, m.activateControl()
		}
		m.openDetail()
		return m, nil
	case "r":
		if m.view == viewProviders {
			return m, m.reloadProviders()
		}
		return m, m.reloadRules()
	case "u":
		if m.view == viewProviders {
			return m, m.updateFocusedProvider()
		}
	case "ctrl+u":
		if m.view == viewProviders && len(m.providers) > 0 {
			return m, func() tea.Msg {
				return ui.ActionIntentMsg{
					Action: ui.ActionUpdateAllProviders, Page: ui.PageRules, Capability: protocol.CapabilityRuleProviders, Key: "providers:update-all",
					Title: ui.UpdateAllProvidersTitle, Object: ui.AllRuleProviders,
					Impact: ui.UpdateAllProvidersImpact, Rollback: ui.UpdateAllProvidersRollback,
					Execute: m.updateAllProviders(),
				}
			}
		}
	}
	return m, nil
}

func (m *Model) View() string {
	rulesTab := ui.RulesTabLabel
	providersTab := ui.RuleProvidersTabLabel
	if m.view == viewRules {
		rulesTab = "[" + ui.RulesTabLabel + "]"
	} else {
		providersTab = "[" + ui.RuleProvidersTabLabel + "]"
	}
	var filterA, filterB string
	if m.view == viewRules {
		filterA = fmt.Sprintf("%s: %s", ui.TypeLabel, valueOr(m.typeFilter, ui.FilterAllLabel))
		filterB = fmt.Sprintf("%s: %s", ui.TargetLabel, valueOr(m.targetFilter, ui.FilterAllLabel))
	} else {
		filterA = fmt.Sprintf("%s: %s", ui.BehaviorLabel, valueOr(m.behaviorFilter, ui.FilterAllLabel))
		filterB = fmt.Sprintf("%s: %s", ui.StatusLabel, valueOr(m.statusFilter, ui.FilterAllLabel))
	}
	controlFocused := m.contentFocused && m.focus.kind == focusControl
	control := ui.RenderControlStrip(m.theme, []string{rulesTab, providersTab, filterA, filterB}, m.controlIndex, controlFocused, "  ")
	inner := ui.FullSectionInner(m.layoutWidth())
	textW := ui.SectionTextWidth(inner)
	searchFocused := m.searching || (m.contentFocused && m.focus.kind == focusSearch)
	searchBar := ui.RenderSearchBar(m.theme, m.query, ui.SearchPlaceholder, searchFocused, m.queryCursor, textW)
	controlsBody := control + "\n" + searchBar
	if m.lastError != "" {
		controlsBody += "\n" + m.theme.Muted.Render(m.lastError)
	}
	controls := ui.RenderBorderedSection(m.theme, ui.ControlsSectionTitle, controlsBody, inner)

	var listLines []string
	var listN int
	rulesView := m.view == viewRules
	if rulesView {
		listLines = m.renderRules()
		listN = len(m.VisibleIndexes())
	} else {
		listLines = m.renderProviders()
		listN = len(m.visibleProviderIndexes())
	}
	listTitle := ui.FormatRulesTitle(rulesView, listN)
	list := ui.RenderBorderedSection(m.theme, listTitle, strings.Join(listLines, "\n"), inner)

	listPos, listFocused := 0, m.focus.kind == focusRow && listN > 0
	if listFocused {
		listPos = m.focus.row + 1
	}
	listIndicator := m.theme.Muted.Render(ui.FormatPositionIndicator(listFocused, listPos, listN))
	listStatus := ui.PadCell(listIndicator, inner, ui.AlignRight)

	content := controls + "\n" + list + "\n" + listStatus
	if m.detail != nil {
		content += "\n\n" + m.theme.Dialog.Render(m.theme.Title.Render(m.detail.title)+"\n\n"+m.detail.body+"\n\n"+ui.EscCloseHint)
	}
	return content
}

func (m *Model) activateControl() tea.Cmd {
	switch m.controlIndex {
	case 0:
		m.view = viewRules
	case 1:
		m.view = viewProviders
	case 2:
		if m.view == viewRules {
			m.typeFilter = cycleValue(m.typeFilter, ruleValues(m.rules, func(rule protocol.Rule) string { return rule.Type }))
		} else {
			m.behaviorFilter = cycleValue(m.behaviorFilter, providerValues(m.providers, func(provider protocol.RuleProvider) string { return provider.Behavior }))
		}
	case 3:
		if m.view == viewRules {
			m.targetFilter = cycleValue(m.targetFilter, ruleValues(m.rules, func(rule protocol.Rule) string { return rule.Proxy }))
		} else {
			m.statusFilter = cycleValue(m.statusFilter, providerValues(m.providers, func(provider protocol.RuleProvider) string { return provider.Status }))
		}
	}
	m.reconcileFocus()
	return nil
}

const rulesColGap = 2

// rulesColumnSpec: priorities are protective — 4 cols (36 + 6 gaps = 42)
// always fit the narrowest content width, so no column ever drops in
// practice; the priority order just pins what would go first if it did.
// Type is a flex column: MinWidth 12 covers the common DomainSuffix, and it
// grows toward MaxWidth 18 to fit DomainKeyword (and any future longer tokens
// from upstream mihomo) instead of truncating at a fixed width (issue #29).
func (m *Model) rulesColumnSpec() []ui.TableColumn {
	return []ui.TableColumn{
		{ID: "num", Title: "#", MinWidth: 4, MaxWidth: 4, Flex: 0, Align: ui.AlignRight, Priority: 4},
		{ID: "type", Title: ui.TypeLabel, MinWidth: 12, MaxWidth: 18, Flex: 1, Priority: 3},
		{ID: "payload", Title: ui.PayloadLabel, MinWidth: 12, Flex: 3, Priority: 1},
		{ID: "target", Title: ui.TargetLabel, MinWidth: 8, MaxWidth: 16, Flex: 1, Priority: 2},
	}
}

func (m *Model) layoutWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 100
}

func (m *Model) sectionTextWidth() int {
	return ui.SectionTextWidth(ui.FullSectionInner(m.layoutWidth()))
}

// rulesChrome is the page chrome outside table rows: Controls section
// (top-title + control strip + search + bottom = 4), List section (top-title +
// bottom = 2), the header/rule lines returned by render* (= 2), plus the
// position indicator line below the list (= 1) — 9 accounted, 10 leaves one
// spare row at the tightest layouts.
const rulesChrome = 10

func (m *Model) renderRules() []string {
	// Fit columns inside the list section body (focus marker budget).
	cols, widths := ui.FitPriorityColumns(m.rulesColumnSpec(), max(24, m.sectionTextWidth()-2), rulesColGap)
	header, ruleLine := ui.RenderHeaderRow(m.theme, cols, widths, rulesColGap, -1, false)
	lines := []string{"  " + header, "  " + ruleLine}
	indexes := m.VisibleIndexes()
	if len(indexes) == 0 {
		return append(lines, m.theme.Muted.Render(ui.NoMatchingRules))
	}
	focused := 0
	if m.focus.kind == focusRow {
		focused = m.focus.row
	}
	start, end := ui.VisibleWindow(len(indexes), m.height, rulesChrome, false, focused)
	for visibleRow := start; visibleRow < end; visibleRow++ {
		item := m.rules[indexes[visibleRow]]
		rowFocused := m.focus.kind == focusRow && m.focus.row == visibleRow
		marker := ui.FocusPrefix(rowFocused)
		num := ui.PadCell(fmt.Sprintf("%d", indexes[visibleRow]+1), widths[0], ui.AlignRight)
		// Semantic type/target colors always; RowFocus chrome waits on content focus.
		typ := ui.PadCell(ui.StyleRuleType(m.theme, item.Type), widths[1], ui.AlignLeft)
		payload := ui.PadCell(item.Payload, widths[2], ui.AlignLeft)
		target := ui.PadCell(ui.StyleProxyTarget(m.theme, item.Proxy), widths[3], ui.AlignLeft)
		line := marker + ui.JoinCells([]string{num, typ, payload, target}, rulesColGap)
		if rowFocused && m.contentFocused {
			line = ui.ApplyFocusStyle(line, m.theme.RowFocus)
		}
		lines = append(lines, line)
	}
	return lines
}

// providerColumnSpec: display order is the array order (unchanged); drop
// order follows Priority — name 7 > type 6 > count 5 > status 4 > behavior 3
// > format 2 > updated 1. The old ClassifyContentWidth two-tier switch is
// replaced by continuous priority dropping (design R2).
func (m *Model) providerColumnSpec() []ui.TableColumn {
	return []ui.TableColumn{
		{ID: "name", Title: ui.NameLabel, MinWidth: 10, Flex: 2, Priority: 7},
		{ID: "type", Title: ui.TypeLabel, MinWidth: 6, MaxWidth: 10, Flex: 0, Priority: 6},
		{ID: "behavior", Title: ui.BehaviorLabel, MinWidth: 8, MaxWidth: 12, Flex: 0, Priority: 3},
		{ID: "format", Title: ui.FormatLabel, MinWidth: 6, MaxWidth: 10, Flex: 0, Priority: 2},
		{ID: "count", Title: ui.RulesCountLabel, MinWidth: 5, MaxWidth: 6, Flex: 0, Align: ui.AlignRight, Priority: 5},
		{ID: "updated", Title: ui.UpdatedLabel, MinWidth: 14, MaxWidth: 16, Flex: 0, Priority: 1},
		{ID: "status", Title: ui.StatusLabel, MinWidth: 8, MaxWidth: 12, Flex: 1, Priority: 4},
	}
}

func (m *Model) renderProviders() []string {
	cols, widths := ui.FitPriorityColumns(m.providerColumnSpec(), max(24, m.sectionTextWidth()-2), rulesColGap)
	header, ruleLine := ui.RenderHeaderRow(m.theme, cols, widths, rulesColGap, -1, false)
	lines := []string{"  " + header, "  " + ruleLine}
	indexes := m.visibleProviderIndexes()
	if len(indexes) == 0 {
		return append(lines, m.theme.Muted.Render(ui.NoMatchingRuleProviders))
	}
	focused := 0
	if m.focus.kind == focusRow {
		focused = m.focus.row
	}
	start, end := ui.VisibleWindow(len(indexes), m.height, rulesChrome, false, focused)
	for visibleRow := start; visibleRow < end; visibleRow++ {
		provider := m.providers[indexes[visibleRow]]
		rowFocused := m.focus.kind == focusRow && m.focus.row == visibleRow
		marker := ui.FocusPrefix(rowFocused)
		status := provider.Status
		if m.pending[provider.Name] {
			status = ui.PendingLabel
		}
		updated := ui.MissingValue
		if !provider.UpdatedAt.IsZero() {
			updated = provider.UpdatedAt.Local().Format("2006-01-02 15:04")
		}
		// Semantic type/status colors always; RowFocus chrome waits on content focus.
		typeText := ui.StyleRuleType(m.theme, provider.Type)
		statusText := ui.StyleProviderStatus(m.theme, status)
		values := map[string]string{
			"name":     provider.Name,
			"type":     typeText,
			"behavior": m.theme.Muted.Render(provider.Behavior),
			"format":   m.theme.Muted.Render(provider.Format),
			"count":    fmt.Sprintf("%d", provider.RuleCount),
			"updated":  m.theme.Muted.Render(updated),
			"status":   statusText,
		}
		cells := make([]string, 0, len(cols))
		for index, col := range cols {
			cells = append(cells, ui.PadCell(values[col.ID], widths[index], col.Align))
		}
		line := marker + ui.JoinCells(cells, rulesColGap)
		if rowFocused && m.contentFocused {
			line = ui.ApplyFocusStyle(line, m.theme.RowFocus)
		}
		lines = append(lines, line)
	}
	return lines
}

func (m *Model) visibleProviderIndexes() []int {
	indexes := make([]int, 0, len(m.providers))
	visible := []string{"name", "type", "behavior", "format", "rule_count", "updated", "status"}
	for index, provider := range m.providers {
		updated := ui.MissingValue
		if !provider.UpdatedAt.IsZero() {
			updated = provider.UpdatedAt.Local().Format("2006-01-02 15:04")
		}
		status := provider.Status
		if m.pending[provider.Name] {
			status = ui.PendingLabel
		}
		cells := map[string]string{
			"name":       provider.Name,
			"type":       provider.Type,
			"behavior":   provider.Behavior,
			"format":     provider.Format,
			"rule_count": fmt.Sprintf("%d", provider.RuleCount),
			"updated":    updated,
			"status":     status,
		}
		if !ui.MatchVisibleColumns(cells, visible, m.query) {
			continue
		}
		if m.behaviorFilter != "" && !strings.EqualFold(provider.Behavior, m.behaviorFilter) {
			continue
		}
		if m.statusFilter != "" && !strings.EqualFold(provider.Status, m.statusFilter) {
			continue
		}
		indexes = append(indexes, index)
	}
	return indexes
}

// moveFocus moves vertical focus. Entering the search bar starts character-input
// mode so typing works without an extra Enter.
func (m *Model) moveFocus(delta int) tea.Cmd {
	count := len(m.VisibleIndexes())
	if m.view == viewProviders {
		count = len(m.visibleProviderIndexes())
	}
	switch m.focus.kind {
	case focusControl:
		if delta > 0 {
			return m.startSearch()
		}
		return nil
	case focusSearch:
		if delta < 0 {
			m.focus = pageFocus{kind: focusControl}
			return nil
		}
		if delta > 0 && count > 0 {
			m.focus = pageFocus{kind: focusRow}
			m.rememberFocusedProvider()
		}
		return nil
	}
	if delta < 0 && m.focus.row == 0 {
		return m.startSearch()
	}
	m.focus.row = min(max(0, m.focus.row+delta), max(0, count-1))
	m.rememberFocusedProvider()
	return nil
}

func (m *Model) reconcileFocus() {
	count := len(m.VisibleIndexes())
	if m.view == viewProviders {
		count = len(m.visibleProviderIndexes())
	}
	if count == 0 {
		m.focus = pageFocus{kind: focusControl}
		if m.view == viewProviders {
			m.focusedProvider = ""
		}
	} else if m.focus.kind == focusRow {
		if m.view == viewProviders && m.focusedProvider != "" {
			for row, index := range m.visibleProviderIndexes() {
				if m.providers[index].Name == m.focusedProvider {
					m.focus.row = row
					return
				}
			}
		}
		m.focus.row = min(m.focus.row, count-1)
		m.rememberFocusedProvider()
	}
}

func (m *Model) rememberFocusedProvider() {
	if m.view != viewProviders || m.focus.kind != focusRow {
		return
	}
	indexes := m.visibleProviderIndexes()
	if m.focus.row >= 0 && m.focus.row < len(indexes) {
		m.focusedProvider = m.providers[indexes[m.focus.row]].Name
	}
}

func (m *Model) openDetail() {
	if m.focus.kind != focusRow {
		return
	}
	if m.view == viewRules {
		indexes := m.VisibleIndexes()
		if m.focus.row >= len(indexes) {
			return
		}
		index := indexes[m.focus.row]
		rule := m.rules[index]
		m.detail = &detailState{title: ui.RuleDetailsTitle, body: fmt.Sprintf("%s: %d\n%s: %s\n%s: %s\n%s: %s", ui.EvaluationOrderLabel, index+1, ui.TypeLabel, rule.Type, ui.PayloadLabel, valueOr(rule.Payload, ui.MissingValue), ui.TargetLabel, rule.Proxy)}
		return
	}
	indexes := m.visibleProviderIndexes()
	if m.focus.row >= len(indexes) {
		return
	}
	provider := m.providers[indexes[m.focus.row]]
	updated := ui.MissingValue
	if !provider.UpdatedAt.IsZero() {
		updated = provider.UpdatedAt.Local().Format(time.RFC3339)
	}
	m.detail = &detailState{title: ui.RuleProviderDetailsTitle, body: fmt.Sprintf("%s: %s\n%s: %s\n%s: %s\n%s: %s\n%s: %d\n%s: %s\n%s: %s", ui.NameLabel, provider.Name, ui.TypeLabel, provider.Type, ui.BehaviorLabel, valueOr(provider.Behavior, ui.MissingValue), ui.FormatLabel, valueOr(provider.Format, ui.MissingValue), ui.RulesCountLabel, provider.RuleCount, ui.UpdatedLabel, updated, ui.StatusLabel, provider.Status)}
}

func (m *Model) updateSearchFocus(message tea.Msg) (ui.Page, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "up":
			m.focus = pageFocus{kind: focusControl}
			return m, nil
		case "down":
			count := len(m.VisibleIndexes())
			if m.view == viewProviders {
				count = len(m.visibleProviderIndexes())
			}
			if count > 0 {
				m.focus = pageFocus{kind: focusRow}
				m.rememberFocusedProvider()
			}
			return m, nil
		}
	}
	if ui.IsTextEditMsg(message) {
		enter := m.startSearch()
		page, edit := m.updateSearch(message)
		return page, tea.Batch(enter, edit)
	}
	return m, nil
}

func (m *Model) updateSearch(message tea.Msg) (ui.Page, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			m.searching = false
			m.reconcileFocus()
			return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
		case "up":
			m.searching = false
			m.focus = pageFocus{kind: focusControl}
			m.reconcileFocus()
			return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
		case "down":
			m.searching = false
			count := len(m.VisibleIndexes())
			if m.view == viewProviders {
				count = len(m.visibleProviderIndexes())
			}
			if count > 0 {
				m.focus = pageFocus{kind: focusRow}
				m.rememberFocusedProvider()
			}
			m.reconcileFocus()
			return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
		}
	}
	value, cursor, handled, command := ui.EditTextField(m.query, m.queryCursor, message, 256)
	if handled {
		m.query = value
		m.queryCursor = cursor
		return m, command
	}
	return m, nil
}

func (m *Model) startSearch() tea.Cmd {
	m.searching = true
	m.focus = pageFocus{kind: focusSearch}
	m.queryCursor = utf8.RuneCountInString(m.query)
	return func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputText} }
}

func (m *Model) reloadRules() tea.Cmd {
	if m.client == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, err := m.client.Rules(ctx)
		return rulesResultMsg{result: result, err: err}
	}
}

func (m *Model) reloadProviders() tea.Cmd {
	if m.client == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, err := m.client.RuleProviders(ctx)
		return providersResultMsg{result: result, err: err}
	}
}

func (m *Model) updateFocusedProvider() tea.Cmd {
	if m.client == nil || m.focus.kind != focusRow {
		return nil
	}
	indexes := m.visibleProviderIndexes()
	if m.focus.row >= len(indexes) {
		return nil
	}
	name := m.providers[indexes[m.focus.row]].Name
	m.pending[name] = true
	operationID := m.newOperationID()
	revision := m.revision
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		request := protocol.MutationRequest{OperationID: operationID}
		if revision != 0 {
			request.IfRevision = &revision
		}
		result, err := m.client.UpdateRuleProvider(ctx, name, request)
		return providerUpdateResultMsg{name: name, revision: result.Revision, err: err}
	}
}

// updateAllProviders returns the command that refreshes every provider. It has
// no presentation side effects: the Root Shell confirmation dispatcher owns the
// pending state, so per-provider pending is not marked until the typed result
// is reconciled. This mirrors the system and subscriptions action-intent paths.
func (m *Model) updateAllProviders() tea.Cmd {
	names := make([]string, 0, len(m.providers))
	for _, provider := range m.providers {
		names = append(names, provider.Name)
	}
	baseID := m.newOperationID()
	revision := m.revision
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(max(1, len(names)))*30*time.Second)
		defer cancel()
		for index, name := range names {
			request := protocol.MutationRequest{OperationID: fmt.Sprintf("%s-%d", baseID, index+1)}
			if revision != 0 {
				request.IfRevision = &revision
			}
			result, err := m.client.UpdateRuleProvider(ctx, name, request)
			if err != nil {
				return providersUpdateAllResultMsg{revision: revision, err: err}
			}
			if result.Revision != 0 {
				revision = result.Revision
			}
		}
		return providersUpdateAllResultMsg{revision: revision}
	}
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func ruleValues(rules []protocol.Rule, value func(protocol.Rule) string) []string {
	values := make([]string, 0)
	seen := make(map[string]struct{})
	for _, rule := range rules {
		candidate := value(rule)
		if candidate == "" {
			continue
		}
		key := strings.ToLower(candidate)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, candidate)
	}
	return values
}

func providerValues(providers []protocol.RuleProvider, value func(protocol.RuleProvider) string) []string {
	values := make([]string, 0)
	seen := make(map[string]struct{})
	for _, provider := range providers {
		candidate := value(provider)
		if candidate == "" {
			continue
		}
		key := strings.ToLower(candidate)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, candidate)
	}
	return values
}

func cycleValue(current string, values []string) string {
	if len(values) == 0 {
		return ""
	}
	if current == "" {
		return values[0]
	}
	for index, value := range values {
		if strings.EqualFold(value, current) {
			if index+1 == len(values) {
				return ""
			}
			return values[index+1]
		}
	}
	return values[0]
}

var fallbackOperationID atomic.Uint64

func defaultOperationID() string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err == nil {
		return "tui-provider-" + hex.EncodeToString(raw)
	}
	return fmt.Sprintf("tui-provider-%d", fallbackOperationID.Add(1))
}
