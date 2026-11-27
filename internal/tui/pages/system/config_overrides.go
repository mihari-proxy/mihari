package system

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"

	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type overrideClient interface {
	ConfigOverrides(context.Context) (protocol.ConfigOverridesStatus, error)
	UpdateConfigOverrides(context.Context, string, protocol.ConfigOverridesUpdateRequest) (protocol.ConfigOverridesStatus, error)
	PreviewConfigOverrides(context.Context, protocol.ConfigOverridesPreviewRequest) (protocol.ConfigOverridesPreview, error)
}

type overrideUI struct {
	importedSaveID                                                   string
	open, loaded, saving, full, reveal, previewFocus, confirmDiscard bool
	reloading                                                        bool
	display                                                          overridePreviewDisplay
	category                                                         string
	menu, selected, top                                              int
	epoch, sequence                                                  uint64
	status                                                           protocol.ConfigOverridesStatus
	draft                                                            protocol.ConfigOverrides
	notice, previewError                                             string
	previewPending                                                   bool
	preview                                                          []protocol.OverridePreviewLine
	cancel                                                           context.CancelFunc
	edit                                                             string
	input                                                            textarea.Model
	yamlRaw                                                          string
}

var overrideEditors = []string{"Authentication", "DNS Override", "YAML fragments"}
var dnsFields = []string{"enable", "ipv6", "enhanced-mode", "nameserver", "default-nameserver", "proxy-server-nameserver", "respect-rules", "fallback"}

type overrideStatusMsg struct {
	epoch  uint64
	status protocol.ConfigOverridesStatus
	save   bool
	err    error
}

func (r overrideStatusMsg) InvalidatesSnapshots() bool        { return r.save && r.err == nil }
func (r overrideStatusMsg) Err() error                        { return r.err }
func (r overrideStatusMsg) Warnings() protocol.WarningOutcome { return r.status.WarningOutcome }

type overridePreviewMsg struct {
	epoch, sequence uint64
	result          protocol.ConfigOverridesPreview
	err             error
}

func (m *Model) openOverrides() tea.Cmd {
	if !m.mutationsEnabled {
		return nil
	}
	m.resetOverrides()
	m.overrides.open = true
	return func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputText} }
}

func (m *Model) resetOverrides() {
	o := &m.overrides
	if o.cancel != nil {
		o.cancel()
	}
	*o = overrideUI{epoch: o.epoch + 1}
}

func (m *Model) loadOverrides(category string) tea.Cmd {
	client, ok := m.client.(overrideClient)
	if !ok {
		return nil
	}
	m.overrides.category = category
	m.overrides.selected = 0
	m.overrides.epoch++
	m.overrides.loaded = false
	m.overrides.notice = ""
	return m.readOverrideStatus(client)
}
func (m *Model) readOverrideStatus(client overrideClient) tea.Cmd {
	epoch, ctx := m.overrides.epoch, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		v, e := client.ConfigOverrides(ctx)
		return ui.PageResultMsg{Page: ui.PageSystem, Result: overrideStatusMsg{epoch: epoch, status: v, err: e}}
	}
}

func (m *Model) handleOverrideStatus(r overrideStatusMsg) tea.Cmd {
	o := &m.overrides
	if !o.open || o.epoch != r.epoch {
		return nil
	}
	o.saving = false
	if r.err != nil {
		o.notice = actionErrorDetail(r.err, r.err.Error()) + " (F2: details)"
		var api protocol.APIError
		if r.save && errors.As(r.err, &api) && api.Code == protocol.CodeRevisionConflict {
			if client, ok := m.client.(overrideClient); ok {
				o.reloading = true
				o.saving = true
				return m.readOverrideStatus(client)
			}
		}
		o.reloading = false
		return nil
	}
	o.loaded = true
	o.status = r.status
	if o.reloading {
		o.reloading = false
		o.notice = "Settings changed. Draft retained; review before saving again."
		return m.refreshOverridePreview()
	}
	o.draft = r.status.Config.Clone()
	if r.save {
		o.notice = "Saved"
		return tea.Batch(m.refreshOverridePreview(), func() tea.Msg { return ui.RuntimeRevisionMsg{Revision: r.status.Revision} })
	}
	return m.refreshOverridePreview()
}

func (m *Model) saveOverrides() tea.Cmd {
	o := &m.overrides
	if !o.loaded || o.saving || !m.mutationsEnabled {
		return nil
	}
	client, ok := m.client.(overrideClient)
	if !ok {
		return nil
	}
	o.saving = true
	o.notice = ""
	draft := o.draft.Clone()
	rev, ctx, epoch, category := o.status.Revision, m.ctx, o.epoch, o.category
	id := o.importedSaveID
	if id == "" {
		id = m.newOperationID()
	}
	o.importedSaveID = ""
	req := protocol.ConfigOverridesUpdateRequest{OperationID: id, IfRevision: &rev}
	if category == "dns" {
		req.DNS = draft.DNS
	} else {
		req.Fragments = draft.Fragments
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		v, e := client.UpdateConfigOverrides(ctx, category, req)
		return ui.PageResultMsg{Page: ui.PageSystem, Result: overrideStatusMsg{epoch: epoch, status: v, err: e, save: true}}
	}
}

func (m *Model) refreshOverridePreview() tea.Cmd {
	o := &m.overrides
	if o.cancel != nil {
		o.cancel()
	}
	client, ok := m.client.(overrideClient)
	if !ok || !o.loaded {
		return nil
	}
	o.sequence++
	ctx, cancel := context.WithCancel(m.ctx)
	o.cancel = cancel
	o.previewPending = true
	o.previewError = ""
	epoch, sequence := o.epoch, o.sequence
	draft := m.inputDraft()
	req := protocol.ConfigOverridesPreviewRequest{Category: o.category, Full: o.full, Reveal: o.reveal}
	if o.category == "dns" {
		req.DNS = draft.DNS
	} else {
		req.Fragments = draft.Fragments
	}
	return func() tea.Msg {
		defer cancel()
		timer := time.NewTimer(authPreviewDebounce)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil
		}
		value, e := loadOverridePreview(ctx, client, req)
		if ctx.Err() != nil {
			return nil
		}
		return ui.PageResultMsg{Page: ui.PageSystem, Result: overridePreviewMsg{epoch, sequence, value, e}}
	}
}

func loadOverridePreview(ctx context.Context, client overrideClient, req protocol.ConfigOverridesPreviewRequest) (protocol.ConfigOverridesPreview, error) {
	var result protocol.ConfigOverridesPreview
	for {
		pageCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		page, err := client.PreviewConfigOverrides(pageCtx, req)
		cancel()
		if err != nil {
			return protocol.ConfigOverridesPreview{}, err
		}
		if req.Offset == 0 {
			result = page
			result.Lines = nil
		} else if req.Snapshot != page.Snapshot || page.Revision != result.Revision {
			return protocol.ConfigOverridesPreview{}, fmt.Errorf("preview input changed between pages")
		}
		for _, line := range page.Lines {
			if line.Continued && len(result.Lines) > 0 {
				result.Lines[len(result.Lines)-1].Text += line.Text
			} else {
				result.Lines = append(result.Lines, line)
			}
		}
		if page.NextOffset == 0 {
			return result, nil
		}
		if page.NextOffset <= req.Offset || page.Snapshot == "" {
			return protocol.ConfigOverridesPreview{}, fmt.Errorf("invalid continuation in configuration preview")
		}
		req.Offset, req.Snapshot = page.NextOffset, page.Snapshot
	}
}

func (m *Model) handleOverridePreview(r overridePreviewMsg) {
	o := &m.overrides
	if !o.open || o.epoch != r.epoch || o.sequence != r.sequence {
		return
	}
	o.previewPending = false
	if r.err != nil {
		o.previewError = actionErrorDetail(r.err, r.err.Error())
		return
	}
	if r.result.Revision != o.status.Revision {
		o.previewError = "Settings changed. Reopen to review the current configuration."
		return
	}
	o.preview = r.result.Lines
	o.display = overridePreviewDisplay{}
}

func (m *Model) exitOverrideEditor() tea.Cmd {
	menu := m.overrides.menu
	m.resetOverrides()
	m.overrides.open = true
	m.overrides.menu = menu
	return nil
}

func (m *Model) updateOverrides(msg tea.Msg) tea.Cmd {
	o := &m.overrides
	key, ok := msg.(tea.KeyPressMsg)
	if o.category == "" {
		if !ok {
			return nil
		}
		switch key.String() {
		case "up":
			o.menu = max(0, o.menu-1)
		case "down":
			o.menu = min(len(overrideEditors)-1, o.menu+1)
		case "enter":
			switch o.menu {
			case 0:
				return m.openAuth()
			case 1:
				return m.loadOverrides("dns")
			case 2:
				return m.loadOverrides("fragments")
			}
		case "esc":
			m.resetOverrides()
			return func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
		}
		return nil
	}
	k := ""
	if ok {
		k = key.String()
	}
	if o.saving {
		return nil
	}
	if o.confirmDiscard {
		switch k {
		case "y", "enter":
			return m.exitOverrideEditor()
		case "n", "esc":
			o.confirmDiscard = false
		}
		return nil
	}
	switch k {
	case "ctrl+o":
		o.full = !o.full
		o.top = 0
		o.preview = nil
		o.display = overridePreviewDisplay{}
		return m.refreshOverridePreview()
	case "ctrl+r":
		o.reveal = !o.reveal
		o.preview = nil
		o.display = overridePreviewDisplay{}
		return m.refreshOverridePreview()
	}
	if o.edit != "" {
		return m.updateOverrideInput(msg)
	}
	if !ok {
		return nil
	}
	if k == "esc" {
		if o.loaded && !reflect.DeepEqual(o.draft, o.status.Config) {
			o.confirmDiscard = true
			return nil
		}
		return m.exitOverrideEditor()
	}
	if !o.loaded {
		return nil
	}
	switch k {
	case "ctrl+s":
		return m.saveOverrides()
	case "tab", "shift+tab":
		o.previewFocus = !o.previewFocus
		return nil
	}
	if o.previewFocus {
		switch k {
		case "up":
			o.top = max(0, o.top-1)
		case "down":
			o.top++
		case "pgup":
			o.top = max(0, o.top-max(1, m.height/2))
		case "pgdown":
			o.top += max(1, m.height/2)
		}
		return nil
	}
	count := len(dnsFields)
	if o.category == "fragments" {
		count = len(o.draft.Fragments)
	}
	switch k {
	case "up":
		o.selected = max(0, o.selected-1)
		return nil
	case "down":
		o.selected = min(max(0, count-1), o.selected+1)
		return nil
	}
	if o.category == "dns" {
		return m.editDNSField(k)
	}
	return m.editFragment(k)
}

func dnsBool(key string) bool { return key == "enable" || key == "ipv6" || key == "respect-rules" }
func dnsChoices(key string) []string {
	if dnsBool(key) {
		return []string{"inherit", "true", "false", "delete"}
	}
	if key == "enhanced-mode" {
		return []string{"inherit", "fake-ip", "redir-host", "normal", "delete"}
	}
	return []string{"inherit", "set", "append", "remove", "delete"}
}

func dnsChoice(v protocol.DNSFieldOverride) string {
	if v.Operation == "" {
		return "inherit"
	}
	if v.Bool != nil {
		return fmt.Sprint(*v.Bool)
	}
	if v.Mode != "" {
		return v.Mode
	}
	return v.Operation
}

func (m *Model) editDNSField(k string) tea.Cmd {
	o := &m.overrides
	field := dnsFields[o.selected]
	v := o.draft.DNS[field]
	switch k {
	case "left", "right", "enter":
		options := dnsChoices(field)
		if k == "enter" && !dnsBool(field) && field != "enhanced-mode" {
			return m.beginOverrideInput("servers", strings.Join(v.Servers, "\n"))
		}
		delta := 1
		if k == "left" {
			delta = -1
		}
		i := slices.Index(options, dnsChoice(v))
		next := options[(i+delta+len(options))%len(options)]
		if o.draft.DNS == nil {
			o.draft.DNS = make(map[string]protocol.DNSFieldOverride)
		}
		switch next {
		case "inherit":
			delete(o.draft.DNS, field)
			if len(o.draft.DNS) == 0 {
				o.draft.DNS = nil
			}
		case "true", "false":
			b := next == "true"
			o.draft.DNS[field] = protocol.DNSFieldOverride{Operation: "set", Bool: &b}
		case "fake-ip", "redir-host", "normal":
			o.draft.DNS[field] = protocol.DNSFieldOverride{Operation: "set", Mode: next}
		case "delete":
			o.draft.DNS[field] = protocol.DNSFieldOverride{Operation: "delete"}
		default:
			o.draft.DNS[field] = protocol.DNSFieldOverride{Operation: next, Servers: v.Servers}
		}
		o.notice = ""
		return m.refreshOverridePreview()
	}
	return nil
}
