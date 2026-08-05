package subscriptions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

type Client interface {
	Subscriptions(context.Context) (protocol.SubscriptionList, error)
	AddSubscription(context.Context, protocol.SubscriptionAddRequest) (protocol.SubscriptionResult, error)
	RefreshSubscription(context.Context, string, protocol.MutationRequest) (protocol.SubscriptionResult, error)
	UseSubscription(context.Context, string, protocol.MutationRequest) (protocol.SubscriptionResult, error)
	SetSubscriptionEnabled(context.Context, string, protocol.SubscriptionEnabledRequest) (protocol.SubscriptionResult, error)
	UpdateSubscription(context.Context, string, protocol.SubscriptionUpdateRequest) (protocol.SubscriptionResult, error)
	RemoveSubscription(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
}

type focusKind uint8

const (
	focusEmpty focusKind = iota
	focusRow
)

type pageFocus struct {
	kind focusKind
	id   string
}

// loadPhase is the user-visible readiness of a subscription relative to mihomo.
// Only loadLive means the active profile has been applied so the core can use it.
type loadPhase uint8

const (
	loadDisabled loadPhase = iota
	loadMissing
	loadFetching
	loadApplying
	loadWorking
	loadCached
	loadStale
	loadFailed
	loadLive
)

type row struct {
	active      string
	name        string
	state       string
	load        string
	traffic     string
	lastSuccess string
	nextRefresh string
	spinning    bool
}

func (r row) Render(theme ui.Theme) string {
	active := fmt.Sprintf("%-6s", r.active)
	if strings.TrimSpace(r.active) == "●" {
		// Dual-channel: glyph + Success color; pad after styled rune to keep columns stable.
		active = theme.Success.Render("●") + "     "
	}
	load := r.load
	if r.spinning {
		load = theme.Warning.Render(load)
	}
	traffic := r.traffic
	if traffic == "" {
		traffic = ui.MissingValue
	}
	return fmt.Sprintf("%s  %-18s  %-9s  %-11s  %-18s  %-12s  %s",
		active, truncate(r.name, 18), r.state, load, truncate(traffic, 18), r.lastSuccess, r.nextRefresh)
}

// resolveLoadPhase maps catalog + local pending work onto intermediate load states.
// Live is reserved for: active catalog selection + valid cache + no last error
// (daemon applied that generation to mihomo on the last successful use/refresh).
func resolveLoadPhase(subscription protocol.Subscription, active bool, pending string, now time.Time, globalInterval string) loadPhase {
	switch pending {
	case "refresh":
		return loadFetching
	case "use":
		return loadApplying
	case "toggle", "edit", "add":
		return loadWorking
	}
	if !subscription.Enabled {
		return loadDisabled
	}
	if subscription.LastError != "" {
		return loadFailed
	}
	if !subscription.Cached {
		return loadMissing
	}
	interval := effectiveInterval(subscription.Interval, globalInterval)
	stale := !subscription.UpdatedAt.IsZero() && interval > 0 && !now.Before(subscription.UpdatedAt.Add(interval))
	if stale {
		return loadStale
	}
	if active {
		return loadLive
	}
	return loadCached
}

func loadPhaseLabel(phase loadPhase, clock time.Time) (label string, spinning bool) {
	switch phase {
	case loadFetching:
		return ui.SpinnerLabel(clock, ui.LoadFetchingLabel), true
	case loadApplying:
		return ui.SpinnerLabel(clock, ui.LoadApplyingLabel), true
	case loadWorking:
		return ui.SpinnerLabel(clock, ui.LoadWorkingLabel), true
	case loadLive:
		return ui.LoadLiveState, false
	case loadCached:
		return ui.LoadCachedState, false
	case loadMissing:
		return ui.LoadMissingState, false
	case loadStale:
		return ui.LoadStaleState, false
	case loadFailed:
		return ui.LoadFailedState, false
	default:
		return ui.DisabledLabel, false
	}
}

func rowFrom(subscription protocol.Subscription, active bool, pending string, now, clock time.Time, globalInterval string) row {
	state := ui.DisabledLabel
	if subscription.Enabled {
		state = ui.EnabledLabel
	}
	phase := resolveLoadPhase(subscription, active, pending, now, globalInterval)
	load, spinning := loadPhaseLabel(phase, clock)
	interval := effectiveInterval(subscription.Interval, globalInterval)
	stale := subscription.Cached && (subscription.LastError != "" || (!subscription.UpdatedAt.IsZero() && interval > 0 && !now.Before(subscription.UpdatedAt.Add(interval))))
	lastSuccess := ui.MissingValue
	if !subscription.UpdatedAt.IsZero() {
		lastSuccess = relativeTime(now, subscription.UpdatedAt)
	}
	next := ui.ManualLabel
	switch {
	case !subscription.Enabled:
		next = ui.DisabledLabel
	case !subscription.AutoRefresh:
		next = ui.ManualLabel
	case stale || subscription.LastError != "" || subscription.UpdatedAt.IsZero():
		next = ui.RetryPendingLabel
	case interval > 0:
		next = relativeTime(now, subscription.UpdatedAt.Add(interval))
	}
	marker := ""
	if active {
		marker = "●"
	}
	traffic := ui.FormatSubscriptionTraffic(subscription.Upload, subscription.Download, subscription.Total)
	return row{
		active: marker, name: subscription.Name, state: state, load: load,
		traffic: traffic, lastSuccess: lastSuccess, nextRefresh: next, spinning: spinning,
	}
}

type detailState struct{ subscription protocol.Subscription }

type Model struct {
	client         Client
	newOperationID func() string
	now            func() time.Time
	subscriptions  []protocol.Subscription
	activeID       string
	globalInterval string
	revision       uint64
	focus          pageFocus
	pending        map[string]string
	form           *formModel
	formID         string
	formRevision   uint64
	detail         *detailState
	lastError      string
	width          int
	height         int
	theme          ui.Theme
	contentFocused bool
	// loadSpinClock advances braille frames while any row has in-flight work.
	loadSpinClock time.Time
	loadSpinning  bool
	loadSpinGen   uint64
}

// loadSpinTickMsg advances braille frames while any subscription mutation is pending.
type loadSpinTickMsg struct {
	t   time.Time
	gen uint64
}

type startLoadSpinMsg struct{ gen uint64 }

const loadSpinInterval = 100 * time.Millisecond

type mutationKind uint8

const (
	mutationAdd mutationKind = iota
	mutationUpdate
	mutationRefresh
	mutationUse
	mutationToggle
	mutationRemove
)

type mutationResultMsg struct {
	kind   mutationKind
	id     string
	result protocol.SubscriptionResult
	remove protocol.MutationResult
	err    error
}

type subscriptionsResultMsg struct {
	result protocol.SubscriptionList
	err    error
}

type refreshAllResultMsg struct {
	revision uint64
	err      error
}

func New(client Client, newOperationID func() string, now func() time.Time) *Model {
	if newOperationID == nil {
		newOperationID = defaultOperationID
	}
	if now == nil {
		now = time.Now
	}
	return &Model{client: client, newOperationID: newOperationID, now: now, pending: make(map[string]string), theme: ui.DefaultTheme()}
}

func (m *Model) ID() ui.PageID { return ui.PageSubscriptions }

// SetContentFocused reports whether the root shell has given keyboard focus to this page.
func (m *Model) SetContentFocused(focused bool) { m.contentFocused = focused }

func (m *Model) SetSize(width, height int) { m.width, m.height = width, height }

func (m *Model) FocusFirst() {
	if len(m.subscriptions) == 0 {
		m.focus = pageFocus{}
	} else if m.index(m.focus.id) < 0 {
		m.focus = pageFocus{kind: focusRow, id: m.subscriptions[0].ID}
	}
}

func (m *Model) SetSubscriptions(result protocol.SubscriptionList) {
	previousIndex := m.index(m.focus.id)
	m.subscriptions = append([]protocol.Subscription(nil), result.Subscriptions...)
	m.activeID, m.globalInterval, m.revision = result.ActiveID, result.GlobalInterval, result.Revision
	if m.index(m.focus.id) < 0 {
		if len(m.subscriptions) == 0 {
			m.focus = pageFocus{}
		} else {
			m.focus = pageFocus{kind: focusRow, id: m.subscriptions[min(max(0, previousIndex), len(m.subscriptions)-1)].ID}
		}
	}
}

func (m *Model) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	switch typed := message.(type) {
	case mutationResultMsg:
		delete(m.pending, typed.id)
		delete(m.pending, "__add")
		if typed.err != nil {
			var apiError protocol.APIError
			if errors.As(typed.err, &apiError) && apiError.Code == protocol.CodeRevisionConflict {
				m.lastError = ui.SubscriptionChangedMessage
				return m, tea.Batch(m.reload(), m.loadSpinCmdIfNeeded())
			}
			m.lastError = subscriptionErrorMessage(typed.err)
			return m, m.loadSpinCmdIfNeeded()
		}
		m.lastError = ""
		if typed.kind == mutationRemove {
			m.revision = typed.remove.Revision
			m.removeLocal(typed.id)
			return m, m.loadSpinCmdIfNeeded()
		}
		m.revision = typed.result.Revision
		m.upsert(typed.result.Subscription)
		if typed.kind == mutationUse {
			m.activeID = typed.result.Subscription.ID
		} else if m.activeID == typed.id && ((typed.kind == mutationToggle && !typed.result.Subscription.Enabled) || (typed.kind == mutationUpdate && !typed.result.Subscription.Cached)) {
			m.activeID = ""
		}
		return m, m.loadSpinCmdIfNeeded()
	case subscriptionsResultMsg:
		if typed.err != nil {
			m.lastError = ui.SubscriptionsUnavailable
		} else {
			m.lastError = ""
			m.SetSubscriptions(typed.result)
		}
		return m, m.loadSpinCmdIfNeeded()
	case refreshAllResultMsg:
		clear(m.pending)
		if typed.err != nil {
			var apiError protocol.APIError
			if errors.As(typed.err, &apiError) && apiError.Code == protocol.CodeRevisionConflict {
				m.lastError = ui.SubscriptionChangedMessage
				return m, tea.Batch(m.reload(), m.loadSpinCmdIfNeeded())
			}
			m.lastError = subscriptionErrorMessage(typed.err)
			return m, m.loadSpinCmdIfNeeded()
		}
		m.lastError = ""
		if typed.revision > 0 {
			m.revision = typed.revision
		}
		return m, tea.Batch(m.reload(), m.loadSpinCmdIfNeeded())
	case startLoadSpinMsg:
		if typed.gen != m.loadSpinGen || !m.hasPendingLoadWork() {
			if typed.gen == m.loadSpinGen {
				m.loadSpinning = false
			}
			return m, nil
		}
		return m, tea.Tick(loadSpinInterval, func(t time.Time) tea.Msg {
			return loadSpinTickMsg{t: t, gen: typed.gen}
		})
	case loadSpinTickMsg:
		if typed.gen != m.loadSpinGen {
			return m, nil
		}
		m.loadSpinClock = typed.t
		if !m.hasPendingLoadWork() {
			m.loadSpinning = false
			return m, nil
		}
		return m, tea.Tick(loadSpinInterval, func(t time.Time) tea.Msg {
			return loadSpinTickMsg{t: t, gen: typed.gen}
		})
	}

	key, ok := message.(tea.KeyPressMsg)
	if m.detail != nil {
		if ok && (key.String() == "esc" || key.String() == "enter") {
			m.detail = nil
		}
		return m, nil
	}
	if m.form != nil {
		return m.updateForm(message)
	}
	if !ok {
		return m, nil
	}
	if key.String() == "a" {
		m.form, m.formID, m.formRevision = newAddForm(), "", m.revision
		return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputText} }
	}
	index := m.index(m.focus.id)
	switch key.String() {
	case "esc":
		return m, func() tea.Msg { return ui.FocusRailMsg{} }
	case "up":
		if index > 0 {
			m.focus.id = m.subscriptions[index-1].ID
		}
	case "down":
		if index >= 0 && index+1 < len(m.subscriptions) {
			m.focus.id = m.subscriptions[index+1].ID
		}
	case "enter":
		if index >= 0 {
			m.detail = &detailState{subscription: m.subscriptions[index]}
		}
	case "e":
		if index >= 0 {
			m.form, m.formID, m.formRevision = newEditForm(m.subscriptions[index]), m.subscriptions[index].ID, m.revision
			return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputText} }
		}
	case "space":
		if index >= 0 {
			return m, m.toggle(m.subscriptions[index])
		}
	case "r":
		if index >= 0 {
			return m, m.refresh(m.subscriptions[index].ID)
		}
	case "ctrl+r":
		if len(m.subscriptions) > 0 {
			return m, func() tea.Msg {
				return ui.ActionIntentMsg{
					Action: ui.ActionRefreshAllSubscriptions, Page: ui.PageSubscriptions, Capability: protocol.CapabilitySubscriptions, Key: "subscriptions:refresh-all",
					Title: ui.RefreshAllSubscriptionsTitle, Object: ui.AllSubscriptionsLabel,
					Impact: ui.RefreshAllSubscriptionsImpact, Rollback: ui.RefreshAllSubscriptionsRollback,
					Execute: m.refreshAll(),
				}
			}
		}
	case "u":
		if index >= 0 {
			return m, m.use(m.subscriptions[index].ID)
		}
	case "d":
		if index >= 0 {
			subscription := m.subscriptions[index]
			operationID, revision := m.newOperationID(), m.revision
			return m, func() tea.Msg {
				return ui.ActionIntentMsg{
					Action: ui.ActionDeleteSubscription, Page: ui.PageSubscriptions, Capability: protocol.CapabilitySubscriptions, Key: "subscription:delete:" + subscription.ID,
					Title: ui.RemoveSubscriptionTitle, Object: subscription.Name, Impact: ui.RemoveSubscriptionImpact, Rollback: ui.RemoveSubscriptionRollback,
					Execute: m.remove(subscription.ID, operationID, revision),
				}
			}
		}
	}
	return m, nil
}

// FooterHints returns contextual shortcuts for the root shell footer.
func (m *Model) FooterHints() string {
	switch {
	case m.form != nil:
		return ui.FormHelp
	case m.detail != nil:
		return ui.DetailCloseHelp + "  ? help  q quit"
	default:
		return ui.FooterSubscriptions
	}
}

func (m *Model) View() string {
	lines := []string{
		m.theme.Title.Render(ui.SubscriptionsTitle),
		"  Active  Name                State      Load         " + ui.TrafficLabel + "              " + ui.LastUpdateLabel + "  " + ui.NextUpdateLabel,
	}
	if m.lastError != "" {
		lines = append(lines, m.theme.Muted.Render(m.lastError))
	}
	if len(m.subscriptions) == 0 {
		lines = append(lines, m.theme.Muted.Render(ui.NoSubscriptions))
	}
	clock := m.loadSpinClock
	if clock.IsZero() {
		clock = m.now()
	}
	wall := m.now()
	for _, subscription := range m.subscriptions {
		rowFocused := m.focus.kind == focusRow && m.focus.id == subscription.ID
		marker := "  "
		if rowFocused {
			marker = ui.FocusMarker
		}
		entry := rowFrom(subscription, subscription.ID == m.activeID, m.pending[subscription.ID], wall, clock, m.globalInterval)
		line := marker + entry.Render(m.theme)
		// Keyboard focus uses RowFocus; business active marker is ● (Success).
		if rowFocused && m.contentFocused {
			line = m.theme.RowFocus.Render(line)
		}
		lines = append(lines, line)
	}
	content := strings.Join(lines, "\n")
	if m.form != nil {
		title := ui.AddSubscriptionTitle
		if m.form.kind == formEdit {
			title = ui.EditSubscriptionTitle
		}
		content = m.modal(title, m.form.View()+"\n\n"+ui.FormHelp)
	} else if m.detail != nil {
		content = m.modal(ui.SubscriptionDetailsTitle, m.detailView())
	}
	return content
}

func (m *Model) updateForm(message tea.Msg) (ui.Page, tea.Cmd) {
	key, isKey := message.(tea.KeyPressMsg)
	if isKey && key.String() == "enter" {
		if m.form.index+1 < len(m.form.inputs) {
			return m, m.form.move(1)
		}
		if !m.form.valid() || m.client == nil {
			m.lastError = ui.InvalidSubscriptionForm
			return m, nil
		}
		form, id, revision := m.form, m.formID, m.formRevision
		m.form, m.formID, m.formRevision = nil, "", 0
		return m, tea.Batch(func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }, m.submitForm(form, id, revision))
	}
	closed, command := m.form.Update(message)
	if closed {
		m.form, m.formID, m.formRevision = nil, "", 0
		return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
	}
	return m, command
}

func (m *Model) submitForm(form *formModel, id string, revision uint64) tea.Cmd {
	operationID := m.newOperationID()
	if form.kind == formAdd {
		m.pending["__add"] = "add"
		request := form.addRequest(operationID, revision)
		return tea.Batch(func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			result, err := m.client.AddSubscription(ctx, request)
			return mutationResultMsg{kind: mutationAdd, result: result, err: err}
		}, m.loadSpinCmdIfNeeded())
	}
	m.pending[id] = "edit"
	request := form.updateRequest(operationID, revision)
	return tea.Batch(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result, err := m.client.UpdateSubscription(ctx, id, request)
		return mutationResultMsg{kind: mutationUpdate, id: id, result: result, err: err}
	}, m.loadSpinCmdIfNeeded())
}

func (m *Model) toggle(subscription protocol.Subscription) tea.Cmd {
	if m.client == nil {
		return nil
	}
	id, operationID, revision := subscription.ID, m.newOperationID(), m.revision
	m.pending[id] = "toggle"
	return tea.Batch(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result, err := m.client.SetSubscriptionEnabled(ctx, id, protocol.SubscriptionEnabledRequest{OperationID: operationID, IfRevision: &revision, Enabled: !subscription.Enabled})
		return mutationResultMsg{kind: mutationToggle, id: id, result: result, err: err}
	}, m.loadSpinCmdIfNeeded())
}

func (m *Model) refresh(id string) tea.Cmd {
	if m.client == nil {
		return nil
	}
	operationID, revision := m.newOperationID(), m.revision
	m.pending[id] = "refresh"
	return tea.Batch(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := m.client.RefreshSubscription(ctx, id, protocol.MutationRequest{OperationID: operationID, IfRevision: &revision})
		return mutationResultMsg{kind: mutationRefresh, id: id, result: result, err: err}
	}, m.loadSpinCmdIfNeeded())
}

// refreshAll refreshes every subscription in list order. Presentation pending
// is owned by the Root Shell confirmation dispatcher until execute begins.
func (m *Model) refreshAll() tea.Cmd {
	if m.client == nil {
		return nil
	}
	ids := make([]string, 0, len(m.subscriptions))
	for _, subscription := range m.subscriptions {
		ids = append(ids, subscription.ID)
	}
	baseID, revision := m.newOperationID(), m.revision
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(max(1, len(ids)))*30*time.Second)
		defer cancel()
		for index, id := range ids {
			request := protocol.MutationRequest{OperationID: fmt.Sprintf("%s-%d", baseID, index+1)}
			if revision != 0 {
				request.IfRevision = &revision
			}
			result, err := m.client.RefreshSubscription(ctx, id, request)
			if err != nil {
				return refreshAllResultMsg{revision: revision, err: err}
			}
			if result.Revision != 0 {
				revision = result.Revision
			}
		}
		return refreshAllResultMsg{revision: revision}
	}
}

func (m *Model) use(id string) tea.Cmd {
	if m.client == nil {
		return nil
	}
	operationID, revision := m.newOperationID(), m.revision
	m.pending[id] = "use"
	return tea.Batch(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result, err := m.client.UseSubscription(ctx, id, protocol.MutationRequest{OperationID: operationID, IfRevision: &revision})
		return mutationResultMsg{kind: mutationUse, id: id, result: result, err: err}
	}, m.loadSpinCmdIfNeeded())
}

func (m *Model) hasPendingLoadWork() bool {
	return len(m.pending) > 0
}

// loadSpinCmdIfNeeded starts a generation-owned braille spin loop while mutations run.
func (m *Model) loadSpinCmdIfNeeded() tea.Cmd {
	if !m.hasPendingLoadWork() {
		m.loadSpinning = false
		return nil
	}
	if m.loadSpinning {
		return nil
	}
	m.loadSpinGen++
	gen := m.loadSpinGen
	m.loadSpinning = true
	return func() tea.Msg { return startLoadSpinMsg{gen: gen} }
}

// remove returns the command that deletes a subscription. It has no
// presentation side effects: the Root Shell confirmation dispatcher owns the
// pending state, so the row is not marked until the typed result is reconciled.
// This mirrors the rules, system, connections, and setup action-intent paths.
func (m *Model) remove(id, operationID string, revision uint64) tea.Cmd {
	if m.client == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result, err := m.client.RemoveSubscription(ctx, id, protocol.MutationRequest{OperationID: operationID, IfRevision: &revision})
		return mutationResultMsg{kind: mutationRemove, id: id, remove: result, err: err}
	}
}

func (m *Model) reload() tea.Cmd {
	if m.client == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, err := m.client.Subscriptions(ctx)
		return subscriptionsResultMsg{result: result, err: err}
	}
}

func (m *Model) upsert(subscription protocol.Subscription) {
	if index := m.index(subscription.ID); index >= 0 {
		m.subscriptions[index] = subscription
		return
	}
	m.subscriptions = append(m.subscriptions, subscription)
	m.focus = pageFocus{kind: focusRow, id: subscription.ID}
}

func (m *Model) removeLocal(id string) {
	index := m.index(id)
	if index < 0 {
		return
	}
	m.subscriptions = append(m.subscriptions[:index], m.subscriptions[index+1:]...)
	if m.activeID == id {
		m.activeID = ""
	}
	if len(m.subscriptions) == 0 {
		m.focus = pageFocus{}
	} else {
		m.focus = pageFocus{kind: focusRow, id: m.subscriptions[min(index, len(m.subscriptions)-1)].ID}
	}
}

func (m *Model) index(id string) int {
	for index := range m.subscriptions {
		if m.subscriptions[index].ID == id {
			return index
		}
	}
	return -1
}

func (m *Model) detailView() string {
	subscription := m.detail.subscription
	errorState := ui.MissingValue
	if subscription.LastError != "" {
		errorState = subscription.LastError
	}
	phase := resolveLoadPhase(subscription, subscription.ID == m.activeID, m.pending[subscription.ID], m.now(), m.globalInterval)
	load, _ := loadPhaseLabel(phase, m.now())
	traffic := ui.FormatSubscriptionTraffic(subscription.Upload, subscription.Download, subscription.Total)
	if traffic == "" {
		traffic = ui.MissingValue
	}
	return fmt.Sprintf("%s: %s\n%s: %s\n%s: %t\n%s: %s\n%s: %s\n%s: %t\n%s: %s\n%s: %s\n%s: %s\n\n%s",
		ui.NameLabel, subscription.Name,
		ui.StatusLabel, enabledLabel(subscription.Enabled),
		ui.AutoRefreshLabel, subscription.AutoRefresh,
		ui.LoadLabel, load,
		ui.TrafficLabel, traffic,
		ui.CacheLabel, subscription.Cached,
		ui.IntervalLabel, valueOr(subscription.Interval, ui.GlobalLabel),
		ui.LastUpdateLabel, formatTimestamp(subscription.UpdatedAt),
		ui.LastErrorLabel, errorState,
		ui.EscCloseHint)
}

// subscriptionErrorMessage returns a user-visible failure reason without leaking
// URL tokens. Prefer the protocol message when available.
func subscriptionErrorMessage(err error) string {
	var apiError protocol.APIError
	if errors.As(err, &apiError) && strings.TrimSpace(apiError.Message) != "" {
		return apiError.Message
	}
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		return err.Error()
	}
	return ui.SubscriptionOperationFailed
}

func (m *Model) modal(title, body string) string {
	content := m.theme.Dialog.Width(min(72, max(36, m.width-6))).Render(m.theme.Title.Render(title) + "\n\n" + body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

func effectiveInterval(value, global string) time.Duration {
	if value == "" {
		value = global
	}
	duration, _ := time.ParseDuration(value)
	return duration
}

func relativeTime(now, target time.Time) string {
	delta := target.Sub(now)
	future := delta > 0
	if delta < 0 {
		delta = -delta
	}
	var value string
	switch {
	case delta >= 24*time.Hour:
		value = fmt.Sprintf("%dd", int(delta/(24*time.Hour)))
	case delta >= time.Hour:
		value = fmt.Sprintf("%dh", int(delta/time.Hour))
	case delta >= time.Minute:
		value = fmt.Sprintf("%dm", int(delta/time.Minute))
	default:
		value = "now"
	}
	if value == "now" {
		return value
	}
	if future {
		return "in " + value
	}
	return value + " ago"
}

func enabledLabel(enabled bool) string {
	if enabled {
		return ui.EnabledLabel
	}
	return ui.DisabledLabel
}

func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return ui.MissingValue
	}
	return value.Local().Format(time.RFC3339)
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
	return string(runes[:width-1]) + "…"
}

var fallbackOperationID atomic.Uint64

func defaultOperationID() string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err == nil {
		return "tui-subscription-" + hex.EncodeToString(raw)
	}
	return fmt.Sprintf("tui-subscription-%d", fallbackOperationID.Add(1))
}
