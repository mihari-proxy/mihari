package rules

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
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
	case "/":
		m.searching = true
		return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputText} }
	case "left":
		if m.focus.kind == focusControl {
			if m.controlIndex > 0 {
				m.controlIndex--
				return m, nil
			}
			return m, func() tea.Msg { return ui.FocusRailMsg{} }
		}
	case "right":
		if m.focus.kind == focusControl {
			m.controlIndex = min(3, m.controlIndex+1)
		}
		return m, nil
	case "up":
		m.move(-1)
		return m, nil
	case "down":
		m.move(1)
		return m, nil
	case "enter":
		if m.focus.kind == focusControl {
			return m, m.activateControl()
		}
		if m.focus.kind == focusSearch {
			m.searching = true
			return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputText} }
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
	tabs := "[" + ui.RulesTabLabel + "]  " + ui.RuleProvidersTabLabel
	if m.view == viewProviders {
		tabs = ui.RulesTabLabel + "  [" + ui.RuleProvidersTabLabel + "]"
	}
	control := tabs
	if m.view == viewRules {
		control += fmt.Sprintf("  %s: %s  %s: %s", ui.TypeLabel, valueOr(m.typeFilter, ui.FilterAllLabel), ui.TargetLabel, valueOr(m.targetFilter, ui.FilterAllLabel))
	} else {
		control += fmt.Sprintf("  %s: %s  %s: %s", ui.BehaviorLabel, valueOr(m.behaviorFilter, ui.FilterAllLabel), ui.StatusLabel, valueOr(m.statusFilter, ui.FilterAllLabel))
	}
	searchFocused := m.searching || m.focus.kind == focusSearch
	searchBar := ui.RenderSearchBar(m.theme, m.query, ui.SearchPlaceholder, searchFocused, m.width)
	lines := []string{m.theme.Title.Render(control), searchBar}
	if m.lastError != "" {
		lines = append(lines, m.theme.Muted.Render(m.lastError))
	}
	if m.view == viewRules {
		lines = append(lines, m.renderRules()...)
	} else {
		lines = append(lines, m.renderProviders()...)
	}
	content := strings.Join(lines, "\n")
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

func (m *Model) renderRules() []string {
	lines := []string{fmt.Sprintf("    #  %-16s  %-39s  %s", ui.TypeLabel, ui.PayloadLabel, ui.TargetLabel)}
	indexes := m.VisibleIndexes()
	if len(indexes) == 0 {
		return append(lines, m.theme.Muted.Render(ui.NoMatchingRules))
	}
	for visibleRow, index := range indexes {
		rule := m.rules[index]
		marker := "  "
		rowFocused := m.focus.kind == focusRow && m.focus.row == visibleRow
		if rowFocused {
			marker = ui.FocusMarker
		}
		line := fmt.Sprintf("%s%4d  %-16s  %-39s  %s", marker, index+1, truncate(rule.Type, 16), truncate(rule.Payload, 39), rule.Proxy)
		if rowFocused && m.contentFocused {
			line = m.theme.RowSelected.Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func (m *Model) renderProviders() []string {
	lines := []string{fmt.Sprintf("  %-16s  %-8s  %-13s  %-10s  %5s  %-19s  %s", ui.NameLabel, ui.TypeLabel, ui.BehaviorLabel, ui.FormatLabel, ui.RulesCountLabel, ui.UpdatedLabel, ui.StatusLabel)}
	indexes := m.visibleProviderIndexes()
	if len(indexes) == 0 {
		return append(lines, m.theme.Muted.Render(ui.NoMatchingRuleProviders))
	}
	for visibleRow, index := range indexes {
		provider := m.providers[index]
		marker := "  "
		rowFocused := m.focus.kind == focusRow && m.focus.row == visibleRow
		if rowFocused {
			marker = ui.FocusMarker
		}
		status := provider.Status
		if m.pending[provider.Name] {
			status = ui.PendingLabel
		}
		updated := ui.MissingValue
		if !provider.UpdatedAt.IsZero() {
			updated = provider.UpdatedAt.Local().Format("2006-01-02 15:04")
		}
		line := fmt.Sprintf("%s%-16s  %-8s  %-13s  %-10s  %5d  %-19s  %s", marker, truncate(provider.Name, 16), provider.Type, provider.Behavior, provider.Format, provider.RuleCount, updated, status)
		if rowFocused && m.contentFocused {
			line = m.theme.RowSelected.Render(line)
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

func (m *Model) move(delta int) {
	count := len(m.VisibleIndexes())
	if m.view == viewProviders {
		count = len(m.visibleProviderIndexes())
	}
	switch m.focus.kind {
	case focusControl:
		if delta > 0 {
			m.focus = pageFocus{kind: focusSearch}
		}
		return
	case focusSearch:
		if delta < 0 {
			m.focus = pageFocus{kind: focusControl}
			return
		}
		if delta > 0 && count > 0 {
			m.focus = pageFocus{kind: focusRow}
			m.rememberFocusedProvider()
		}
		return
	}
	if delta < 0 && m.focus.row == 0 {
		m.focus = pageFocus{kind: focusSearch}
		return
	}
	m.focus.row = min(max(0, m.focus.row+delta), max(0, count-1))
	m.rememberFocusedProvider()
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

func (m *Model) updateSearch(message tea.Msg) (ui.Page, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "enter", "esc":
			m.searching = false
			m.reconcileFocus()
			return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
		}
	}
	value, handled, command := ui.EditTextField(m.query, message, 256)
	if handled {
		m.query = value
		return m, command
	}
	return m, nil
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

func truncate(value string, width int) string {
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
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
