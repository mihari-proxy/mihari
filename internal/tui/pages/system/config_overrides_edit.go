package system

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func (m *Model) editFragment(k string) tea.Cmd {
	o := &m.overrides
	switch k {
	case "a":
		return m.beginOverrideInput("new name", "")
	case "i":
		return m.beginOverrideInput("import path", "")
	}
	if len(o.draft.Fragments) == 0 {
		return nil
	}
	f := &o.draft.Fragments[o.selected]
	switch k {
	case "enter":
		return m.beginOverrideInput("YAML", f.YAML)
	case "r":
		return m.beginOverrideInput("name", f.Name)
	case "space":
		f.Enabled = !f.Enabled
	case "d":
		o.draft.Fragments = slices.Delete(o.draft.Fragments, o.selected, o.selected+1)
		o.selected = min(o.selected, max(0, len(o.draft.Fragments)-1))
	case "K", "J":
		next := o.selected + 1
		if k == "K" {
			next = o.selected - 1
		}
		if next < 0 || next >= len(o.draft.Fragments) {
			return nil
		}
		o.draft.Fragments[o.selected], o.draft.Fragments[next] = o.draft.Fragments[next], o.draft.Fragments[o.selected]
		o.selected = next
	default:
		return nil
	}
	o.notice = ""
	return m.refreshOverridePreview()
}

func (m *Model) beginOverrideInput(field, value string) tea.Cmd {
	o := &m.overrides
	o.edit = field
	o.input = textarea.New()
	o.input.Prompt = ""
	o.input.ShowLineNumbers = false
	o.input.MaxHeight = 0
	o.input.MaxWidth = 0
	width, height := m.overridePaneDimensions()
	o.input.SetWidth(width)
	o.input.SetHeight(max(1, height-2))
	if field != "YAML" && field != "servers" {
		o.input.SetHeight(1)
	}
	if field == "YAML" {
		o.yamlRaw = value
		value = yamlEditorDisplay(value)
	}
	o.input.SetValue(value)
	return o.input.Focus()
}

func (m *Model) inputDraft() protocol.ConfigOverrides {
	o := &m.overrides
	draft := o.draft.Clone()
	if o.edit == "" {
		return draft
	}
	switch o.edit {
	case "YAML":
		draft.Fragments[o.selected].YAML = o.yamlRaw
	case "name":
		draft.Fragments[o.selected].Name = o.input.Value()
	case "servers":
		key := dnsFields[o.selected]
		value := draft.DNS[key]
		if value.Operation == "" || value.Operation == "delete" {
			value.Operation = "set"
		}
		value.Servers = nil
		for _, line := range strings.Split(o.input.Value(), "\n") {
			if server := strings.TrimSpace(line); server != "" {
				value.Servers = append(value.Servers, server)
			}
		}
		if draft.DNS == nil {
			draft.DNS = make(map[string]protocol.DNSFieldOverride)
		}
		draft.DNS[key] = value
	}
	return draft
}

func (m *Model) updateOverrideInput(msg tea.Msg) tea.Cmd {
	o := &m.overrides
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			o.edit = ""
			return m.refreshOverridePreview()
		case "ctrl+s", "tab", "enter":
			if key.String() == "enter" && (o.edit == "YAML" || o.edit == "servers") {
				break
			}
			field, value := o.edit, o.input.Value()
			if field == "new name" || field == "name" || field == "import path" {
				if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\n\r") {
					o.notice = "Enter a single non-empty line"
					return nil
				}
			}
			if field == "import path" {
				o.edit = ""
				return m.importOverride(value)
			}
			if field == "new name" {
				o.draft.Fragments = append(o.draft.Fragments, protocol.OverrideFragment{ID: m.newOperationID(), Name: value, Enabled: true})
				o.selected = len(o.draft.Fragments) - 1
				return m.beginOverrideInput("YAML", "")
			}
			o.draft = m.inputDraft()
			o.edit = ""
			o.notice = ""
			if key.String() == "ctrl+s" {
				return m.saveOverrides()
			}
			return m.refreshOverridePreview()
		}
	}
	if o.edit == "YAML" {
		return m.updateYAMLInput(msg)
	}
	previous := o.input.Value()
	var cmd tea.Cmd
	o.input, cmd = o.input.Update(msg)
	if o.input.Value() != previous && (o.edit == "servers" || o.edit == "name") {
		return tea.Batch(cmd, m.refreshOverridePreview())
	}
	return cmd
}

type overrideImportMsg struct {
	epoch         uint64
	name, content string
	err           error
}

func (r overrideImportMsg) Err() error { return r.err }

func (m *Model) importOverride(path string) tea.Cmd {
	epoch, ctx := m.overrides.epoch, m.ctx
	m.overrides.saving = true
	m.overrides.notice = "Importing…"
	return func() tea.Msg {
		content, err := readOverrideFile(ctx, path)
		return ui.PageResultMsg{Page: ui.PageSystem, Result: overrideImportMsg{epoch: epoch, name: filepath.Base(path), content: content, err: err}}
	}
}

func readOverrideFile(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect YAML file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("expected a regular YAML file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open YAML file: %w", err)
	}
	defer file.Close()
	const maxBytes = protocol.MaxControlRequestBytes
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read YAML file: %w", err)
	}
	if len(data) > maxBytes {
		return "", fmt.Errorf("file exceeds the 1 MiB control request limit")
	}
	return string(data), ctx.Err()
}

func (m *Model) handleOverrideImport(r overrideImportMsg) tea.Cmd {
	o := &m.overrides
	if !o.open || r.epoch != o.epoch {
		return nil
	}
	o.saving = false
	if r.err != nil {
		o.notice = actionErrorDetail(r.err, r.err.Error()) + " (F2: details)"
		return nil
	}
	fragment := protocol.OverrideFragment{ID: m.newOperationID(), Name: r.name, YAML: r.content, Enabled: true}
	fragments := append(slices.Clip(o.draft.Fragments), fragment)
	saveID := o.importedSaveID
	if saveID == "" {
		saveID = m.newOperationID()
	}
	// Continuation metadata travels in the URL. Test the largest actual preview
	// body (both flags enabled) and the next save, including all previous drafts.
	preview := protocol.ConfigOverridesPreviewRequest{Category: "fragments", Fragments: fragments, Full: true, Reveal: true}
	save := protocol.ConfigOverridesUpdateRequest{OperationID: saveID, IfRevision: &o.status.Revision, Fragments: fragments}
	for _, request := range []any{preview, save} {
		raw, err := json.Marshal(request)
		if err != nil {
			o.notice = "Could not encode import: " + err.Error()
			return nil
		}
		if len(raw) > protocol.MaxControlRequestBytes {
			o.notice = "Import exceeds the 1 MiB control request limit for the complete fragment list"
			return nil
		}
	}
	o.draft.Fragments = fragments
	o.importedSaveID = saveID
	o.selected = len(o.draft.Fragments) - 1
	o.notice = "Imported into draft"
	return m.refreshOverridePreview()
}
