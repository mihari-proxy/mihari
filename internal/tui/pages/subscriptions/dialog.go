package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type savePhase uint8

const (
	saveEditing savePhase = iota
	saveSending
	saveConflict
	saveUnknown
	saveRetryConfirm
	saveChecking
	saveRunning
)

type revealResultMsg struct {
	cancelled  bool
	epoch      uint64
	connection uint64
	id         string
	result     protocol.SubscriptionURL
	err        error
}
type saveCheckMsg struct {
	cancelled  bool
	epoch, seq uint64
	purpose    savePhase
	state      string
	list       protocol.SubscriptionList
	matches    bool
	err        error
}
type savePollMsg struct{ epoch uint64 }

// Message identifies asynchronous replies owned by the subscriptions page.
type Message interface{ SubscriptionPageMessage() }

func (revealResultMsg) SubscriptionPageMessage()        {}
func (saveCheckMsg) SubscriptionPageMessage()           {}
func (savePollMsg) SubscriptionPageMessage()            {}
func (mutationResultMsg) SubscriptionPageMessage()      {}
func (subscriptionsResultMsg) SubscriptionPageMessage() {}
func (refreshAllResultMsg) SubscriptionPageMessage()    {}
func (startLoadSpinMsg) SubscriptionPageMessage()       {}
func (loadSpinTickMsg) SubscriptionPageMessage()        {}

func (m *Model) openForm(f *formModel, id string) tea.Cmd {
	if m.revealCancel != nil {
		m.revealCancel()
	}
	if m.queryCancel != nil {
		m.queryCancel()
	}
	m.dialogEpoch++
	m.form = f
	m.formID = id
	m.formRevision = m.revision
	m.saveState = saveEditing
	m.dialogNote = ""
	m.dialogScroll = 0
	m.confirmYes = false
	m.ensureFormFocus()
	return m.readFormURL()
}

// readFormURL retries a guarded reveal without resetting the current draft or dialog identity.
func (m *Model) readFormURL() tea.Cmd {
	f, id := m.form, m.formID
	if f.urlTouched || f.urlBaseline != "" {
		return nil
	}
	if m.revealCancel != nil {
		m.revealCancel()
	}
	reader, ok := m.client.(interface {
		SubscriptionURL(context.Context, string) (protocol.SubscriptionURL, error)
	})
	if id == "" {
		return nil
	}
	if !ok {
		f.inputs[1].Placeholder = "Unable to read URL. Paste to replace."
		return nil
	}
	epoch := m.dialogEpoch
	connection := m.connectionEpoch
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	m.revealCancel = cancel
	return func() tea.Msg {
		defer cancel()
		result, err := reader.SubscriptionURL(ctx, id)
		return revealResultMsg{cancelled: diagnostics.NormalCancellation(ctx, err), epoch: epoch, connection: connection, id: id, result: result, err: err}
	}
}

func (m *Model) closeForm() tea.Cmd {
	if m.revealCancel != nil {
		m.revealCancel()
		m.revealCancel = nil
	}
	if m.queryCancel != nil {
		m.queryCancel()
		m.queryCancel = nil
	}
	m.dialogEpoch++
	m.form = nil
	m.formID = ""
	m.formRevision = 0
	m.saveOperation = ""
	m.saveState = saveEditing
	return nil
}

func (m *Model) finishSave(result mutationResultMsg) tea.Cmd {
	delete(m.pending, result.id)
	delete(m.pending, "__add")
	if result.err == nil {
		m.revision = result.result.Revision
		m.upsert(result.result.Subscription)
		m.lastError = ""
		if result.kind == mutationAdd && result.result.Subscription.LastError != "" {
			m.lastError = "Subscription added, but the initial refresh failed. Press r to retry."
		}
		return tea.Batch(m.closeForm(), m.loadSpinCmdIfNeeded())
	}
	var api protocol.APIError
	if errors.As(result.err, &api) && api.Code == protocol.CodeRevisionConflict {
		m.saveState = saveConflict
		m.confirmYes = false
		m.dialogNote = "The configuration changed while you were editing. Overwrite your changed fields?"
		return m.loadSpinCmdIfNeeded()
	}
	// Without a confirmed daemon rejection, transport loss cannot establish failure.
	unknown := !errors.As(result.err, &api) || api.Code == protocol.CodeDaemonUnavailable || errors.Is(result.err, context.DeadlineExceeded)
	var outcome interface{ OutcomeUnknown() bool }
	if errors.As(result.err, &outcome) {
		unknown = outcome.OutcomeUnknown()
	}
	if unknown {
		m.saveState = saveUnknown
		m.dialogNote = "Save outcome unknown."
		if m.disconnected {
			m.dialogNote += " Reconnect to check the subscription state."
			return nil
		}
		return m.checkSave(saveUnknown)
	}
	m.saveState = saveEditing
	m.dialogNote = ""
	m.form.errorText = subscriptionErrorMessage(result.err)
	m.ensureFormFocus()
	return tea.Batch(m.loadSpinCmdIfNeeded(), m.readFormURL())
}

func (m *Model) updateSaveKeys(message tea.Msg) tea.Cmd {
	key, ok := message.(tea.KeyPressMsg)
	if !ok || m.saveState == saveSending {
		return nil
	}
	name := key.String()
	if name == "esc" {
		if m.saveState == saveRetryConfirm {
			m.saveState = saveUnknown
			m.dialogNote = "Unable to confirm the previous save."
			return nil
		}
		if m.saveState != saveConflict {
			m.lastError = "Closing does not cancel the save."
		}
		return m.closeForm()
	}
	switch m.saveState {
	case saveConflict, saveRetryConfirm:
		if name == "left" || name == "right" || name == "tab" || name == "shift+tab" {
			m.confirmYes = !m.confirmYes
		}
		if name == "enter" {
			if !m.confirmYes {
				if m.saveState == saveConflict {
					return m.closeForm()
				}
				m.saveState = saveUnknown
				m.dialogNote = "Unable to confirm the previous save."
				return nil
			}
			return m.checkSave(m.saveState)
		}
	case saveUnknown:
		if name == "enter" && !m.disconnected {
			m.saveState = saveRetryConfirm
			m.confirmYes = false
			m.dialogNote = "The previous save may have succeeded. Submit your changes again?"
			if m.form.kind == formAdd {
				m.dialogNote = "The previous save may have succeeded. Submitting again may create a duplicate subscription. Continue?"
			}
		}
	case saveRunning:
		if name == "enter" && !m.disconnected {
			return m.checkSave(saveUnknown)
		}
	}
	return nil
}

// ObserveConnection resumes read-only verification after a session reconnects.
func (m *Model) ObserveConnection(connected bool) tea.Cmd {
	if connected == m.disconnected {
		m.connectionEpoch++
	}
	m.disconnected = !connected
	if !connected {
		if m.revealCancel != nil {
			m.revealCancel()
		}
		if m.queryCancel != nil {
			m.queryCancel()
			m.querySeq++
		}
		if m.form != nil && (m.saveState == saveChecking || m.saveState == saveRunning || m.saveState == saveUnknown) {
			m.saveState = saveUnknown
			m.dialogNote = "Save outcome unknown. Reconnect to check the subscription state."
		}
		return nil
	}
	if m.form != nil && (m.saveState == saveUnknown || m.saveState == saveRunning) {
		return m.checkSave(saveUnknown)
	}
	return nil
}

func (m *Model) checkSave(purpose savePhase) tea.Cmd {
	if m.client == nil || m.disconnected {
		return nil
	}
	if m.queryCancel != nil {
		m.queryCancel()
	}
	m.querySeq++
	epoch, seq := m.dialogEpoch, m.querySeq
	id, op := m.formID, m.saveOperation
	request := m.form.updateRequest
	var patch protocol.SubscriptionUpdateRequest
	if m.form.kind == formEdit {
		patch = request("", 0)
	}
	m.saveState = saveChecking
	m.dialogNote = "Checking subscription state..."
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	m.queryCancel = cancel
	client := m.client
	return func() tea.Msg {
		defer cancel()
		msg := saveCheckMsg{epoch: epoch, seq: seq, purpose: purpose}
		reply := func() tea.Msg { msg.cancelled = diagnostics.NormalCancellation(ctx, msg.err); return msg }
		if purpose == saveUnknown || purpose == saveRetryConfirm {
			reader, ok := client.(interface {
				OperationStatus(context.Context, string) (protocol.OperationStatus, error)
			})
			if ok {
				status, err := reader.OperationStatus(ctx, op)
				msg.state = status.State
				if err != nil {
					msg.err = err
					return reply()
				}
				if status.State == "running" {
					return reply()
				}
			}
		}
		msg.list, msg.err = client.Subscriptions(ctx)
		if msg.err != nil {
			return reply()
		}
		if purpose == saveUnknown && id != "" {
			for _, p := range msg.list.Subscriptions {
				if p.ID == id {
					msg.matches = patchMatches(patch, p)
					if patch.URL != nil && msg.matches {
						reader, ok := client.(interface {
							SubscriptionURL(context.Context, string) (protocol.SubscriptionURL, error)
						})
						msg.matches = false
						if ok {
							revealed, err := reader.SubscriptionURL(ctx, id)
							msg.matches = err == nil && revealed.URL == *patch.URL
							msg.err = err
						}
					}
				}
			}
		}
		return reply()
	}
}

func patchMatches(r protocol.SubscriptionUpdateRequest, p protocol.Subscription) bool {
	return (r.Name == nil || *r.Name == p.Name) && (r.Interval == nil || *r.Interval == p.Interval) && (r.AutoRefresh == nil || *r.AutoRefresh == p.AutoRefresh) && (r.ProxyMode == nil || *r.ProxyMode == p.ProxyMode)
}

func emptyPatch(r protocol.SubscriptionUpdateRequest) bool {
	return r.Name == nil && r.URL == nil && r.Interval == nil && r.AutoRefresh == nil && r.ProxyMode == nil && r.GlobalInterval == nil
}

// formHelpMode selects root help bindings for the current save phase or field.
func (m *Model) formHelpMode() string {
	if m.form.actionOperation != "" || m.form.actionUncertain {
		return ui.ModeSubscriptionActionWaiting
	}
	switch m.saveState {
	case saveSending:
		return ui.ModeSubscriptionSaving
	case saveChecking, saveRunning:
		return ui.ModeSubscriptionWaiting
	case saveUnknown:
		if m.disconnected {
			return ui.ModeSubscriptionWaiting
		}
		return ui.ModeSubscriptionUnknown
	case saveConflict, saveRetryConfirm:
		return ui.ModeSubscriptionConfirm
	}
	if m.form.isCycle() {
		return ui.ModeSubscriptionCycle
	}
	if m.form.isAction() {
		return ui.ModeSubscriptionAction
	}
	if m.form.index == len(m.form.inputs) {
		return ui.ModeSubscriptionSubmit
	}
	return ui.ModeSubscriptionInput
}

func (m *Model) formFooter() string { return ui.RenderFooter(m.ID(), m.formHelpMode(), ui.FooterOpt{}) }

// HasDialog reports page ownership of all keyboard input, including save waits.
func (m *Model) HasDialog() bool { return m.form != nil }

// updateDialogMessage reconciles asynchronous replies against the current dialog.
// URL reveal preserves a user's draft and manual scroll position.
func (m *Model) updateDialogMessage(message tea.Msg) (bool, tea.Cmd) {
	switch msg := message.(type) {
	case revealResultMsg:
		if m.form != nil && msg.epoch == m.dialogEpoch && msg.connection == m.connectionEpoch && msg.id == m.formID && m.saveState == saveEditing {
			if msg.err == nil {
				m.form.reveal(msg.result.URL)
			} else {
				m.form.inputs[1].Placeholder = "Unable to read URL. Paste to replace."
			}
			if !m.dialogManualScroll {
				m.ensureFormFocus()
			}
		}
		return true, nil
	case savePollMsg:
		if m.form != nil && msg.epoch == m.dialogEpoch && m.saveState == saveRunning && !m.disconnected {
			return true, m.checkSave(saveUnknown)
		}
		return true, nil
	case saveCheckMsg:
		if m.form == nil || msg.epoch != m.dialogEpoch || msg.seq != m.querySeq {
			return true, nil
		}
		if msg.err != nil {
			if msg.purpose == saveConflict {
				m.saveState = saveConflict
				m.confirmYes = false
				m.dialogNote = "Unable to read the latest configuration. Try confirming again."
				return true, nil
			}
			m.saveState = saveUnknown
			m.dialogNote = "Unable to confirm the previous save."
			return true, nil
		}
		if msg.state == "running" {
			m.saveState = saveRunning
			m.dialogNote = "The save is still running. Closing does not cancel the save."
			epoch := m.dialogEpoch
			return true, tea.Tick(time.Second, func(time.Time) tea.Msg { return savePollMsg{epoch: epoch} })
		}
		if msg.list.Revision < m.revision {
			m.saveState = saveUnknown
			m.dialogNote = "Unable to confirm the previous save."
			if msg.purpose == saveConflict {
				m.saveState = saveConflict
				m.confirmYes = false
				m.dialogNote = "The configuration changed while you were editing. Overwrite your changed fields?"
			}
			return true, nil
		}
		m.SetSubscriptions(msg.list)
		if m.formID != "" && m.index(m.formID) < 0 {
			m.lastError = "This subscription no longer exists."
			return true, m.closeForm()
		}
		if msg.purpose != saveUnknown {
			return true, m.submitForm(m.form, m.formID, msg.list.Revision)
		}
		if msg.matches {
			m.lastError = "Your changes are present in the subscription."
			return true, m.closeForm()
		}
		m.saveState = saveUnknown
		m.dialogNote = "Unable to confirm the previous save."
		return true, nil
	}
	return false, nil
}

// formStatus groups catalog status above editable fields, omitting absent errors.
func (m *Model) formStatus() string {
	if m.form.kind != formEdit {
		return ""
	}
	p := m.form.baseline
	if i := m.index(m.formID); i >= 0 {
		p = m.subscriptions[i]
	}
	phase := resolveLoadPhase(p, p.ID == m.activeID, m.pending[p.ID], m.now(), m.globalInterval)
	status, _ := loadPhaseLabel(phase, m.now())
	inUse := m.theme.Muted.Render("Not in use")
	if p.ID == m.activeID {
		inUse = ui.StatusDot(m.theme, ui.TonePositive, "In use")
	}
	traffic := ui.FormatSubscriptionTraffic(p.Upload, p.Download, p.Total)
	if traffic == "" {
		traffic = ui.MissingValue
	}
	cache := "Missing"
	if p.Cached {
		cache = "Available"
	}
	lines := []string{inUse + " · " + ui.ToneStyle(m.theme, phaseTone(phase)).Render(status) + " · " + enabledLabel(p.Enabled)}
	for _, field := range [][2]string{{"Traffic", traffic}, {"Cache", cache}, {"Last update", formatTimestamp(p.UpdatedAt)}, {"Next update", nextRefreshLabel(p, m.now(), m.globalInterval)}} {
		lines = append(lines, m.theme.Muted.Render(fmt.Sprintf("%-14s", field[0]))+field[1])
	}
	if p.LastError != "" {
		lines = append(lines, m.theme.Danger.Render("Last error    "+p.LastError))
	}
	return strings.Join(lines, "\n") + "\n"
}

// Stop cancels page-owned requests when the TUI exits. Cancellation is not rollback.
func (m *Model) Stop() {
	for id := range m.requestCancels {
		m.finishRequest(id)
	}
	if m.revealCancel != nil {
		m.revealCancel()
	}
	if m.queryCancel != nil {
		m.queryCancel()
	}
	if m.saveCancel != nil {
		m.saveCancel()
	}
}

// saveBody renders save progress and confirmation choices without edit controls.
func (m *Model) saveBody() string {
	body := m.dialogNote
	switch m.saveState {
	case saveConflict, saveRetryConfirm:
		yes := "Overwrite"
		if m.saveState == saveRetryConfirm {
			yes = "Submit again"
		}
		if m.confirmYes {
			yes = "[" + yes + "]"
		} else {
			yes += "    [Cancel]"
		}
		if m.confirmYes {
			yes += "    Cancel"
		}
		body += "\n\n" + yes
	case saveUnknown:
		if !m.disconnected {
			body += "\n\nSubmit again"
		}
	}
	return body
}
