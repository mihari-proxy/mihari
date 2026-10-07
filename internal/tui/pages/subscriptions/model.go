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
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
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
	loadOutdated
	loadExpired
)

type row struct {
	active      string
	name        string
	state       string
	load        string
	proxy       string
	traffic     string
	lastSuccess string
	nextRefresh string
	loadTone    ui.StatusTone
	stateTone   ui.StatusTone
}

// cellValue renders one column's value with its semantic styling.
func (r row) cellValue(theme ui.Theme, id string) string {
	switch id {
	case "active":
		if strings.TrimSpace(r.active) == "●" {
			// Active selection marker stays a single Positive dot (no label) so it
			// does not collide with the tone-colored state/load columns.
			return ui.StatusDot(theme, ui.TonePositive, "")
		}
		return ""
	case "name":
		return r.name
	case "state":
		return ui.ToneStyle(theme, r.stateTone).Render(r.state)
	case "load":
		return ui.ToneStyle(theme, r.loadTone).Render(r.load)
	case "proxy":
		return r.proxy
	case "traffic":
		if r.traffic == "" {
			return ui.MissingValue
		}
		return r.traffic
	case "lastSuccess":
		return r.lastSuccess
	case "nextRefresh":
		return ui.ToneStyle(theme, ui.ClassifyStatusTone(r.nextRefresh)).Render(r.nextRefresh)
	default:
		return ui.MissingValue
	}
}

// Render lays the row out by the fitted column set, sharing the widths with
// the header (design S1).
func (r row) Render(theme ui.Theme, cols []ui.TableColumn, widths []int) string {
	cells := make([]string, 0, len(cols))
	for index, col := range cols {
		cells = append(cells, ui.PadCell(r.cellValue(theme, col.ID), widths[index], col.Align))
	}
	return strings.Join(cells, subscriptionColGapText)
}

const subscriptionColGapText = "  "

// phaseTone maps a load phase onto a status tone so the Load column carries
// meaning even when no spinner is running (Live=Positive, Failed=Negative,
// intermediate/cache states=Caution, Disabled=Neutral).
func phaseTone(phase loadPhase) ui.StatusTone {
	switch phase {
	case loadLive:
		return ui.TonePositive
	case loadFailed:
		return ui.ToneNegative
	case loadCached, loadMissing, loadStale, loadOutdated, loadExpired, loadFetching, loadApplying, loadWorking:
		return ui.ToneCaution
	default:
		return ui.ToneNeutral
	}
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
	case "toggle", "edit", "add", "proxy":
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
	if subscription.CacheOutdated {
		return loadOutdated
	}
	if subscription.IntervalRefreshRequired {
		return loadExpired
	}
	interval := effectiveInterval(subscription.Interval, globalInterval)
	stale := !subscription.UpdatedAt.IsZero() && interval > 0 && !now.Before(subscription.UpdatedAt.Add(interval))
	if stale {
		if subscription.AutoRefresh {
			return loadExpired
		}
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
		return "Need refresh", false
	case loadOutdated:
		return "Outdated", false
	case loadExpired:
		return "Expired", false
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
	load, _ := loadPhaseLabel(phase, clock)
	lastSuccess := ui.MissingValue
	if !subscription.UpdatedAt.IsZero() {
		lastSuccess = relativeTime(now, subscription.UpdatedAt)
	}
	next := nextRefreshLabel(subscription, now, globalInterval)
	marker := ""
	if active {
		marker = "●"
	}
	stateTone := ui.ToneNeutral
	if subscription.Enabled {
		stateTone = ui.TonePositive
	}
	// Column traffic uses the compact quota form (e.g. 9G/100G, design S1).
	traffic := ui.FormatSubscriptionTrafficCompact(subscription.Upload, subscription.Download, subscription.Total)
	return row{
		active: marker, name: subscription.Name, state: state, load: load,
		proxy: proxyModeLabel(subscription.ProxyMode), traffic: traffic, lastSuccess: lastSuccess, nextRefresh: next,
		loadTone: phaseTone(phase), stateTone: stateTone,
	}
}

type Model struct {
	client             Client
	newContext         func() (context.Context, context.CancelFunc)
	requestTimeout     time.Duration
	requestCancels     map[string]context.CancelFunc
	newOperationID     func() string
	now                func() time.Time
	subscriptions      []protocol.Subscription
	activeID           string
	globalInterval     string
	revision           uint64
	focus              pageFocus
	pending            map[string]string
	form               *formModel
	formID             string
	formRevision       uint64
	dialogEpoch        uint64
	saveState          savePhase
	saveOperation      string
	confirmYes         bool
	dialogNote         string
	dialogScroll       int
	dialogManualScroll bool
	revealCancel       context.CancelFunc
	queryCancel        context.CancelFunc
	querySeq           uint64
	disconnected       bool
	connectionEpoch    uint64
	saveCancel         context.CancelFunc
	lastError          string
	width              int
	height             int
	theme              ui.Theme
	contentFocused     bool
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
	detailEpoch     uint64
	requestRevision uint64
	cancelled       bool
	kind            mutationKind
	id              string
	result          protocol.SubscriptionResult
	remove          protocol.MutationResult
	operation       logging.OperationMetadata
	err             error
}

// Err implements the shell's action-outcome contract so subscription mutations
// are classified Succeeded/Failed in the Recent operations ledger.
func (m mutationResultMsg) Err() error { return m.err }

var _ interface{ Err() error } = mutationResultMsg{}

type subscriptionsResultMsg struct {
	result protocol.SubscriptionList
	err    error
}

type refreshAllResultMsg struct {
	cancelled   bool
	warnings    protocol.WarningOutcome
	operationID string
	revision    uint64
	err         error
}

type canceledRefreshAllMsg struct{ operationID string }

// Err implements the shell's action-outcome contract so bulk refreshes are
// classified Succeeded/Failed in the Recent operations ledger.
func (m refreshAllResultMsg) Err() error { return m.err }

var _ interface{ Err() error } = refreshAllResultMsg{}

func New(client Client, newOperationID func() string, now func() time.Time) *Model {
	if newOperationID == nil {
		newOperationID = defaultOperationID
	}
	if now == nil {
		now = time.Now
	}
	return &Model{client: client, newOperationID: newOperationID, now: now, pending: make(map[string]string), theme: ui.DefaultTheme(),
		newContext:     func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
		requestTimeout: controlclient.SubscriptionMutationTimeout, requestCancels: make(map[string]context.CancelFunc)}
}

// SetContextFactory binds subscription work to the run owner before commands are created.
// The factory must return a cancelable scope without a per-item deadline.
func (m *Model) SetContextFactory(factory func() (context.Context, context.CancelFunc)) {
	if factory != nil {
		m.newContext = factory
	}
}

func (m *Model) subscriptionContext() (context.Context, context.CancelFunc) {
	owner, cancelOwner := m.newContext()
	ctx, cancel := context.WithTimeout(owner, m.requestTimeout)
	return ctx, func() { cancel(); cancelOwner() }
}

func (m *Model) finishRequest(id string) {
	if cancel := m.requestCancels[id]; cancel != nil {
		cancel()
		delete(m.requestCancels, id)
	}
}

func (m *Model) ID() ui.PageID { return ui.PageSubscriptions }

// SetContentFocused reports whether the root shell has given keyboard focus to this page.
func (m *Model) SetContentFocused(focused bool) { m.contentFocused = focused }

func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
	if m.form != nil && m.saveState == saveEditing {
		m.ensureFormFocus()
	}
}

func (m *Model) layoutWidth() int {
	if m.width > 0 {
		return m.width
	}
	// Default wide enough for the fixed subscription table header columns.
	return 120
}

func (m *Model) FocusFirst() {
	if len(m.subscriptions) == 0 {
		m.focus = pageFocus{}
	} else if m.index(m.focus.id) < 0 {
		m.focus = pageFocus{kind: focusRow, id: m.subscriptions[0].ID}
	}
}

// SetSubscriptions refreshes catalog state while preserving form drafts and manual scrolling.
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
	if m.form != nil && m.saveState == saveEditing && !m.dialogManualScroll {
		m.ensureFormFocus()
	}
}

func (m *Model) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	if handled, cmd := m.updateDialogMessage(message); handled {
		return m, cmd
	}
	switch typed := message.(type) {
	case canceledRefreshAllMsg:
		m.finishRequest(typed.operationID)
		return m, nil
	case mutationResultMsg:
		m.finishRequest(typed.operation.ID)
		if typed.detailEpoch != 0 {
			return m, m.finishDetailAction(typed)
		}
		if m.form != nil && m.saveState == saveSending && typed.operation.ID == m.saveOperation {
			return m, m.finishSave(typed)
		}
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
		m.finishRequest(typed.operationID)
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
	if m.form != nil {
		return m.updateForm(message)
	}
	if !ok {
		return m, nil
	}
	if key.String() == "a" {
		return m, m.openForm(newAddForm(), "")
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
			return m, m.openForm(newEditForm(m.subscriptions[index]), m.subscriptions[index].ID)
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
			baseID := m.newOperationID()
			execute := m.refreshAllWithID(baseID)
			return m, func() tea.Msg {
				return ui.ActionIntentMsg{
					Action: ui.ActionRefreshAllSubscriptions, Page: ui.PageSubscriptions, Capability: protocol.CapabilitySubscriptions, Key: "subscriptions:refresh-all",
					Title: ui.RefreshAllSubscriptionsTitle, Object: ui.AllSubscriptionsLabel,
					Impact: ui.RefreshAllSubscriptionsImpact, Rollback: ui.RefreshAllSubscriptionsRollback,
					Execute: execute,
					Cancel: func() tea.Msg {
						return ui.PageResultMsg{Page: ui.PageSubscriptions, Result: canceledRefreshAllMsg{operationID: baseID}}
					},
				}
			}
		}
	case "u":
		if index >= 0 {
			return m, m.use(m.subscriptions[index].ID)
		}
	case "p":
		if index >= 0 {
			return m, m.cycleProxy(m.subscriptions[index])
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

func (m *Model) HelpMode() string {
	switch {
	case m.form != nil:
		return m.formHelpMode()
	default:
		return ""
	}
}

// FooterHints returns contextual shortcuts for the root shell footer.
func (m *Model) FooterHints() string {
	if m.form != nil {
		return m.formFooter()
	}
	return ui.RenderFooter(m.ID(), m.HelpMode(), ui.FooterOpt{})
}

// subscriptionColumns is the checked table definition (design S1 table):
// name is highest priority, nextRefresh drops first.
func (m *Model) subscriptionColumns() []ui.TableColumn {
	// Grow only to the content's visible width, leaving surplus space on the
	// right instead of pushing related fields apart on wide terminals.
	nameWidth, trafficWidth, modeWidth := 10, 11, 6
	for _, subscription := range m.subscriptions {
		nameWidth = max(nameWidth, lipgloss.Width(subscription.Name))
		traffic := ui.FormatSubscriptionTrafficCompact(subscription.Upload, subscription.Download, subscription.Total)
		trafficWidth = max(trafficWidth, lipgloss.Width(traffic))
		modeWidth = max(modeWidth, lipgloss.Width(proxyModeLabel(subscription.ProxyMode)))
	}
	return []ui.TableColumn{
		{ID: "name", Title: ui.NameLabel, MinWidth: 10, MaxWidth: min(nameWidth, 40), Flex: 3, Priority: 8},
		{ID: "active", Title: "InUse", MinWidth: 5, Flex: 0, Priority: 7, Align: ui.AlignCenter},
		{ID: "state", Title: "Enabled", MinWidth: 8, Flex: 0, Priority: 6},
		{ID: "load", Title: "Status", MinWidth: 12, Flex: 0, Priority: 5},
		{ID: "proxy", Title: "Mode", MinWidth: modeWidth, Flex: 0, Priority: 4},
		{ID: "traffic", Title: ui.TrafficLabel, MinWidth: 11, MaxWidth: min(trafficWidth, 24), Flex: 1, Priority: 3},
		{ID: "lastSuccess", Title: ui.LastUpdateLabel, MinWidth: 11, Flex: 0, Priority: 2},
		{ID: "nextRefresh", Title: ui.NextUpdateLabel, MinWidth: 11, Flex: 0, Priority: 1},
	}
}

// subscriptionWidths fits the columns to the section body; header and rows
// share the same result. Budget = textW−2 (focus marker prefix): the bordered
// section clips body lines at textW, so marker + columns must fit exactly.
// At 100 terminal columns the 7-column set's 78 min width exceeds the 76
// budget, so nextRefresh (lowest priority) drops — the design's 7-column
// figure assumes an unclipped budget, which the section frame cannot allow.
func (m *Model) subscriptionWidths() ([]ui.TableColumn, []int) {
	textW := ui.SectionTextWidth(ui.FullSectionInner(m.layoutWidth()))
	avail := max(20, textW-2)
	return ui.FitPriorityColumns(m.subscriptionColumns(), avail, 2)
}

// View renders the subscription list with compact columns and full-width row
// focus, or the active add/edit form.
func (m *Model) View() string {
	inner := ui.FullSectionInner(m.layoutWidth())
	textWidth := ui.SectionTextWidth(inner)
	cols, widths := m.subscriptionWidths()
	header, _ := ui.RenderHeaderRow(m.theme, cols, widths, 2, -1, false)
	// Fill the section independently of the compact, content-sized columns.
	rule := m.theme.SurfaceBorder.Render(strings.Repeat("─", max(0, textWidth-2)))
	bodyLines := []string{"  " + header, "  " + rule}
	if m.lastError != "" {
		bodyLines = append(bodyLines, m.theme.Muted.Render(m.lastError))
	}
	if len(m.subscriptions) == 0 {
		bodyLines = append(bodyLines, m.theme.Muted.Render(ui.NoSubscriptions))
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
		line := marker + entry.Render(m.theme, cols, widths)
		// Keyboard focus uses RowFocus; business active marker is ● (Success).
		if rowFocused && m.contentFocused {
			line = ui.PadCell(line, textWidth, ui.AlignLeft)
			line = ui.ApplyFocusStyle(line, m.theme.RowFocus)
		}
		bodyLines = append(bodyLines, line)
	}
	title := ui.FormatSubscriptionsTitle(len(m.subscriptions))
	content := ui.RenderBorderedSection(m.theme, title, strings.Join(bodyLines, "\n"), inner)
	if m.form != nil {
		formTitle := ui.AddSubscriptionTitle
		if m.form.kind == formEdit {
			formTitle = ui.EditSubscriptionTitle
		}
		content = m.formView(formTitle)
	}
	return content
}

// updateForm routes editing, scrolling, and submission while keeping feedback visible.
func (m *Model) updateForm(message tea.Msg) (ui.Page, tea.Cmd) {
	if m.saveState != saveEditing {
		return m, m.updateSaveKeys(message)
	}
	key, isKey := message.(tea.KeyPressMsg)
	if isKey && key.String() == "pgup" {
		m.dialogManualScroll = true
		m.dialogScroll = max(0, m.dialogScroll-3)
		return m, nil
	}
	if isKey && key.String() == "pgdown" {
		m.dialogManualScroll = true
		m.dialogScroll += 3
		return m, nil
	}
	if m.form.actionOperation != "" || m.form.actionUncertain {
		if isKey && key.String() == "esc" {
			return m, m.closeForm()
		}
		return m, nil
	}
	if isKey && key.String() == "enter" {
		if m.form.isAction() {
			return m, m.submitDetailAction()
		}
		if m.form.index < len(m.form.inputs) {
			cmd := m.form.move(1)
			m.ensureFormFocus()
			return m, cmd
		}
		if !m.form.valid() {
			m.ensureFormFocus()
			failure := m.form.validationErr
			return m, func() tea.Msg { return ui.DiagnosticMsg{Page: ui.PageSubscriptions, Err: failure} }
		}
		if m.client == nil {
			return m, nil
		}
		form, id, revision := m.form, m.formID, m.formRevision
		if form.kind == formEdit && emptyPatch(form.updateRequest("", revision)) {
			return m, m.closeForm()
		}
		return m, m.submitForm(form, id, revision)
	}
	oldIndex := m.form.index
	closed, command := m.form.Update(message)
	if oldIndex != m.form.index {
		m.ensureFormFocus()
	}
	if closed {
		return m, m.closeForm()
	}
	return m, command
}

func (m *Model) submitForm(form *formModel, id string, revision uint64) tea.Cmd {
	operationID := m.newOperationID()
	var ctx context.Context
	var cancel context.CancelFunc
	if form.kind == formAdd {
		ctx, cancel = m.subscriptionContext()
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
	}
	if m.form == form {
		m.saveCancel = cancel
		m.saveState = saveSending
		m.saveOperation = operationID
		m.dialogNote = "Saving..."
		m.form.errorText = ""
	}
	if form.kind == formAdd {
		m.pending["__add"] = "add"
		request := form.addRequest(operationID, revision)
		operation := logging.OperationMetadata{ID: operationID, Name: "subscription.add"}
		return tea.Batch(func() tea.Msg {
			defer cancel()
			result, err := m.client.AddSubscription(logging.WithOperation(ctx, operation), request)
			return mutationResultMsg{cancelled: diagnostics.NormalCancellation(ctx, err), kind: mutationAdd, result: result, operation: operation, err: err}
		}, m.loadSpinCmdIfNeeded())
	}
	m.pending[id] = "edit"
	request := form.updateRequest(operationID, revision)
	operation := logging.OperationMetadata{ID: operationID, Name: "subscription.set"}
	return tea.Batch(func() tea.Msg {
		defer cancel()
		result, err := m.client.UpdateSubscription(logging.WithOperation(ctx, operation), id, request)
		return mutationResultMsg{cancelled: diagnostics.NormalCancellation(ctx, err), kind: mutationUpdate, id: id, result: result, operation: operation, err: err}
	}, m.loadSpinCmdIfNeeded())
}

func (m *Model) toggle(subscription protocol.Subscription) tea.Cmd {
	if m.client == nil {
		return nil
	}
	id, operationID, revision := subscription.ID, m.newOperationID(), m.revision
	operation := logging.OperationMetadata{ID: operationID, Name: "subscription.enabled"}
	m.pending[id] = "toggle"
	return tea.Batch(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ctx = logging.WithOperation(ctx, operation)
		result, err := m.client.SetSubscriptionEnabled(ctx, id, protocol.SubscriptionEnabledRequest{OperationID: operationID, IfRevision: &revision, Enabled: !subscription.Enabled})
		return mutationResultMsg{cancelled: diagnostics.NormalCancellation(ctx, err), kind: mutationToggle, id: id, result: result, operation: operation, err: err}
	}, m.loadSpinCmdIfNeeded())
}

// cycleProxy advances the focused subscription's refresh transport through the
// direct → proxy → auto → direct cycle. The server validates the value; an
// unsupported build surfaces the error via the normal mutation path.
func (m *Model) cycleProxy(subscription protocol.Subscription) tea.Cmd {
	if m.client == nil {
		return nil
	}
	mode := nextProxyMode(subscription.ProxyMode)
	id, operationID, revision := subscription.ID, m.newOperationID(), m.revision
	operation := logging.OperationMetadata{ID: operationID, Name: "subscription.set"}
	m.pending[id] = "proxy"
	return tea.Batch(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ctx = logging.WithOperation(ctx, operation)
		result, err := m.client.UpdateSubscription(ctx, id, protocol.SubscriptionUpdateRequest{OperationID: operationID, IfRevision: &revision, ProxyMode: &mode})
		return mutationResultMsg{cancelled: diagnostics.NormalCancellation(ctx, err), kind: mutationUpdate, id: id, result: result, operation: operation, err: err}
	}, m.loadSpinCmdIfNeeded())
}

// nextProxyMode cycles the per-subscription refresh transport one step forward.
// Values mirror the protocol proxy_mode field (empty = direct).
func nextProxyMode(mode string) string {
	switch mode {
	case "proxy":
		return "auto"
	case "auto":
		return ""
	default:
		return "proxy"
	}
}

// proxyModeLabel renders a stored proxy mode for the Proxy column.
func proxyModeLabel(mode string) string {
	switch mode {
	case "proxy":
		return "PROXY"
	case "auto":
		return "PROXY w Fallback to DIRECT"
	default:
		return "DIRECT"
	}
}

func (m *Model) refresh(id string) tea.Cmd {
	if m.client == nil {
		return nil
	}
	operationID, revision := m.newOperationID(), m.revision
	operation := logging.OperationMetadata{ID: operationID, Name: "subscription.refresh"}
	m.pending[id] = "refresh"
	ctx, cancel := m.subscriptionContext()
	m.requestCancels[operationID] = cancel
	return tea.Batch(func() tea.Msg {
		defer cancel()
		ctx = logging.WithOperation(ctx, operation)
		result, err := m.client.RefreshSubscription(ctx, id, protocol.MutationRequest{OperationID: operationID, IfRevision: &revision})
		return mutationResultMsg{cancelled: diagnostics.NormalCancellation(ctx, err), kind: mutationRefresh, id: id, result: result, operation: operation, err: err}
	}, m.loadSpinCmdIfNeeded())
}

// refreshAll refreshes every subscription in list order. Presentation pending
// is owned by the Root Shell confirmation dispatcher until execute begins.
func (m *Model) refreshAll() tea.Cmd {
	return m.refreshAllWithID(m.newOperationID())
}

func (m *Model) refreshAllWithID(baseID string) tea.Cmd {
	if m.client == nil {
		return nil
	}
	ids := make([]string, 0, len(m.subscriptions))
	for _, subscription := range m.subscriptions {
		ids = append(ids, subscription.ID)
	}
	revision := m.revision
	ctx, cancel := m.newContext()
	m.requestCancels[baseID] = cancel
	timeout := m.requestTimeout
	return func() tea.Msg {
		defer cancel()
		var warnings protocol.WarningOutcome
		for index, id := range ids {
			if err := ctx.Err(); err != nil {
				return refreshAllResultMsg{cancelled: diagnostics.NormalCancellation(ctx, err), warnings: warnings, operationID: baseID, revision: revision, err: err}
			}
			request := protocol.MutationRequest{OperationID: fmt.Sprintf("%s-%d", baseID, index+1)}
			if revision != 0 {
				request.IfRevision = &revision
			}
			itemCtx, cancelItem := context.WithTimeout(ctx, timeout)
			result, err := m.client.RefreshSubscription(logging.WithOperation(itemCtx, logging.OperationMetadata{ID: request.OperationID, Name: "subscription.refresh"}), id, request)
			cancelItem()
			warnings.Append(result.WarningOutcome)
			if err != nil {
				return refreshAllResultMsg{cancelled: diagnostics.NormalCancellation(ctx, err), warnings: warnings, operationID: baseID, revision: revision, err: err}
			}
			if result.Revision != 0 {
				revision = result.Revision
			}
		}
		return refreshAllResultMsg{warnings: warnings, operationID: baseID, revision: revision}
	}
}

func (m *Model) use(id string) tea.Cmd {
	if m.client == nil {
		return nil
	}
	operationID, revision := m.newOperationID(), m.revision
	operation := logging.OperationMetadata{ID: operationID, Name: "subscription.use"}
	m.pending[id] = "use"
	return tea.Batch(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ctx = logging.WithOperation(ctx, operation)
		result, err := m.client.UseSubscription(ctx, id, protocol.MutationRequest{OperationID: operationID, IfRevision: &revision})
		return mutationResultMsg{cancelled: diagnostics.NormalCancellation(ctx, err), kind: mutationUse, id: id, result: result, operation: operation, err: err}
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
	operation := logging.OperationMetadata{ID: operationID, Name: "subscription.remove"}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ctx = logging.WithOperation(ctx, operation)
		result, err := m.client.RemoveSubscription(ctx, id, protocol.MutationRequest{OperationID: operationID, IfRevision: &revision})
		return mutationResultMsg{cancelled: diagnostics.NormalCancellation(ctx, err), kind: mutationRemove, id: id, remove: result, operation: operation, err: err}
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

// nextRefreshLabel predicts the next refresh from UpdatedAt + effective
// interval, mirroring the list column semantics.
func nextRefreshLabel(subscription protocol.Subscription, now time.Time, globalInterval string) string {
	interval := effectiveInterval(subscription.Interval, globalInterval)
	base := subscription.UpdatedAt
	if !subscription.ScheduleFrom.IsZero() {
		base = subscription.ScheduleFrom
	}
	stale := !base.IsZero() && interval > 0 && !now.Before(base.Add(interval))
	switch {
	case !subscription.Enabled:
		return ui.DisabledLabel
	case !subscription.AutoRefresh:
		return ui.ManualLabel
	case stale || base.IsZero():
		return ui.RetryPendingLabel
	case interval > 0:
		return relativeTime(now, base.Add(interval))
	default:
		return ui.ManualLabel
	}
}

// subscriptionErrorMessage returns a user-visible failure reason without leaking
// URL tokens. Prefer the protocol message when available.
func subscriptionErrorMessage(err error) string {
	var apiError protocol.APIError
	if errors.As(err, &apiError) && strings.TrimSpace(apiError.Message) != "" {
		return apiError.Message
	}
	return ui.SubscriptionOperationFailed
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

// formatTimestamp displays local minute precision, or a missing marker for zero time.
func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return ui.MissingValue
	}
	return value.Local().Format("2006-01-02 15:04")
}

var fallbackOperationID atomic.Uint64

func defaultOperationID() string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err == nil {
		return "tui-subscription-" + hex.EncodeToString(raw)
	}
	return fmt.Sprintf("tui-subscription-%d", fallbackOperationID.Add(1))
}
