package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"go.yaml.in/yaml/v3"
)

// PreviewConfigOverrides resolves the draft on the same source as the saved settings.
func (m *Manager) PreviewConfigOverrides(ctx context.Context, req protocol.ConfigOverridesPreviewRequest) (protocol.ConfigOverridesPreview, error) {
	var result protocol.ConfigOverridesPreview
	if err := m.lockMaintenance(ctx); err != nil {
		return result, err
	}
	if m.recoveryOnly || m.coreRecovery.Load() {
		m.unlock()
		return result, protocol.APIError{Code: protocol.CodeInvalidState, Message: "configuration preview unavailable during recovery"}
	}
	saved, generation := m.configInputs()
	catalog := m.loggingCatalog()
	revision := m.store.Load().Revision
	m.unlock()
	input := currentPreviewInput(m.previewSession, generation, catalog)
	if req.Offset < 0 || (req.Offset > 0 && req.Snapshot == "") {
		return result, invalidPreview("invalid preview offset")
	}
	if req.Snapshot != "" {
		start, _, found := strings.Cut(req.Snapshot, "/")
		var err error
		revision, err = strconv.ParseUint(start, 10, 64)
		if !found || err != nil {
			return result, routingConflict()
		}
	}
	identity, err := previewIdentity(req, input, revision)
	if err != nil {
		return result, err
	}
	if req.Snapshot != "" && req.Snapshot != identity {
		return result, routingConflict()
	}
	if req.Offset > 0 {
		if cached := m.previewSnapshots.get(identity); cached != nil {
			return cached.page(req.Offset)
		}
	}
	base, err := m.catalogSourceDocument(catalog)
	if err != nil {
		return result, err
	}
	draft, err := applyPreviewDraft(saved, req)
	if err != nil {
		return result, err
	}
	fields, err := overridePreviewFields(saved, draft, req)
	if err != nil {
		return result, err
	}
	source := "Built-in configuration"
	if index := catalog.Index(catalog.ActiveID); index >= 0 {
		source = previewLabel(catalog.Profiles[index].Name)
	}
	after, err := renderOverridesCandidate(ctx, base, draft, fields, source, req.Reveal)
	if err != nil {
		if req.Category == "auth" && errors.Is(err, subscription.ErrProxyAuthNoUsers) && !reflect.DeepEqual(saved.ProxyAuth, draft.ProxyAuth) {
			// An enabled saved form can itself lack users after a source change.
			// Attribute this failure to the edit only when its baseline still works.
			if _, baselineErr := subscription.Generate(base, nil, saved); baselineErr == nil {
				return result, diagnostics.Wrap(invalidPreview("invalid authentication draft"), err)
			}
		}
		return result, err
	}
	var lines []protocol.ProxyAuthPreviewLine
	if reflect.DeepEqual(saved, draft) {
		lines = make([]protocol.ProxyAuthPreviewLine, len(after))
		for i, entry := range after {
			lines[i] = entry.line
		}
	} else {
		before, baselineErr := renderOverridesCandidate(ctx, base, saved, fields, source, req.Reveal)
		if err := ctx.Err(); err != nil {
			return result, err
		}
		lines = diffAuthPreview(before, after)
		if baselineErr != nil {
			lines = append([]protocol.ProxyAuthPreviewLine{{Text: "# Saved configuration is invalid for this source; showing the draft."}}, lines...)
		}
	}
	result.Revision, result.ActiveID, result.Snapshot = revision, catalog.ActiveID, identity
	result.ActiveName = previewLabel(input.ActiveName)
	snapshot, err := newPreviewSnapshot(ctx, result, lines)
	if err != nil {
		return result, err
	}
	if err = m.lockMaintenance(ctx); err != nil {
		return result, err
	}
	stale := currentPreviewInput(m.previewSession, m.currentConfigGeneration(), m.loggingCatalog()) != input
	m.unlock()
	if stale {
		return protocol.ConfigOverridesPreview{}, routingConflict()
	}
	result, err = snapshot.page(req.Offset)
	if err == nil && (result.NextOffset != 0 || req.Offset > 0) {
		m.previewSnapshots.put(identity, snapshot)
	}
	return result, err
}

func invalidPreview(message string) protocol.APIError {
	return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: message}
}

func applyPreviewDraft(saved config.Settings, req protocol.ConfigOverridesPreviewRequest) (config.Settings, error) {
	after := saved.Clone()
	overrides := saved.EffectiveOverrides()
	switch req.Category {
	case "auth":
		if req.Authentication == nil || req.DNS != nil || req.Fragments != nil {
			return after, invalidPreview("expected only an authentication draft")
		}
		v := resolveProxyAuthPasswords(*req.Authentication, saved.EffectiveProxyAuth())
		if err := config.ValidateProxyAuth(v); err != nil {
			return after, diagnostics.Wrap(invalidPreview("invalid authentication draft"), err)
		}
		after.SetProxyAuth(v)
	case "fragments":
		if req.Authentication != nil || req.DNS != nil {
			return after, invalidPreview("expected only a fragment list")
		}
		if err := config.ValidateOverrides(&protocol.ConfigOverrides{Fragments: req.Fragments}); err != nil {
			return after, diagnostics.Wrap(invalidPreview("invalid fragment list"), err)
		}
		overrides.Fragments = req.Fragments
		after.SetOverrides(overrides)
	case "dns":
		if req.Authentication != nil || req.Fragments != nil {
			return after, invalidPreview("expected only a DNS draft")
		}
		if err := config.ValidateOverrides(&protocol.ConfigOverrides{DNS: req.DNS}); err != nil {
			return after, diagnostics.Wrap(invalidPreview("invalid DNS draft"), err)
		}
		overrides.DNS = req.DNS
		after.SetOverrides(overrides)
	default:
		return after, invalidPreview("unknown override preview category")
	}
	return after, nil
}

type previewInput struct {
	Session                string
	Generation             uint64
	ActiveID               string
	ActiveName             string
	SubscriptionGeneration uint64
}

func currentPreviewInput(session string, generation uint64, c subscription.Catalog) previewInput {
	input := previewInput{Session: session, Generation: generation, ActiveID: c.ActiveID}
	if i := c.Index(c.ActiveID); i >= 0 {
		input.SubscriptionGeneration = c.Profiles[i].Generation
		input.ActiveName = c.Profiles[i].Name
	}
	return input
}

func previewIdentity(req protocol.ConfigOverridesPreviewRequest, input previewInput, revision uint64) (string, error) {
	req.Offset, req.Snapshot = 0, ""
	h := sha256.New()
	err := json.NewEncoder(h).Encode([]any{req, input, revision})
	return strconv.FormatUint(revision, 10) + "/" + hex.EncodeToString(h.Sum(nil)), err
}

func overridePreviewFields(saved, draft config.Settings, req protocol.ConfigOverridesPreviewRequest) (map[string]bool, error) {
	if req.Full {
		return nil, nil
	}
	switch req.Category {
	case "dns":
		return map[string]bool{"dns": true}, nil
	case "auth":
		return map[string]bool{"authentication": true, "skip-auth-prefixes": true}, nil
	}
	before := saved.EffectiveOverrides().Fragments
	after := draft.EffectiveOverrides().Fragments
	changed := !reflect.DeepEqual(before, after)
	fields := map[string]bool{}
	for _, pair := range [][2][]protocol.OverrideFragment{{before, after}, {after, before}} {
		for i, f := range pair[0] {
			if changed && i < len(pair[1]) && f == pair[1][i] {
				continue
			}
			if !f.Enabled {
				continue
			}
			keys, err := subscription.FragmentFields(f)
			if err != nil {
				// A broken saved fragment can still be removed to recover the current source.
				if slices.Contains(after, f) {
					return nil, err
				}
				continue
			}
			for _, key := range keys {
				fields[key] = true
			}
		}
	}
	return fields, nil
}

func renderOverridesCandidate(ctx context.Context, base subscription.Document, settings config.Settings, focus map[string]bool, source string, reveal bool) ([]authPreviewEntry, error) {
	content, origins, err := subscription.GenerateWithSources(base, nil, settings)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	var root yaml.Node
	if err = yaml.Unmarshal(content, &root); err != nil {
		return nil, err
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("expected generated mapping")
	}
	mapping := root.Content[0]
	raw := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	var entries []authPreviewEntry
	hidden := false
	seen := map[string]bool{}
	for i := 0; i < len(mapping.Content); i += 2 {
		key, val := mapping.Content[i], mapping.Content[i+1]
		field := key.Value
		seen[field] = true
		if focus != nil && !focus[field] {
			if !hidden {
				entries = append(entries, authPreviewEntry{identity: "…", line: protocol.ProxyAuthPreviewLine{Text: "…"}})
			}
			hidden = true
			continue
		}
		hidden = false
		start, end := key.Line-1, len(raw)
		if i+2 < len(mapping.Content) {
			end = mapping.Content[i+2].Line - 1
		}
		fieldSource := source
		if s := origins[field]; s != "" {
			fieldSource = s
		}
		switch field {
		case "secret":
			line := "secret: '********'"
			entries = append(entries, authPreviewEntry{identity: field, line: protocol.ProxyAuthPreviewLine{Text: line, Field: field, Source: "Mihari"}})
		case "authentication", "skip-auth-prefixes":
			authLines, err := overrideAuthLines(field, val, reveal, source, origins)
			if err != nil {
				return nil, err
			}
			entries = append(entries, authLines...)
		default:
			annotations := map[int]protocol.ProxyAuthPreviewLine{start: {Field: field, Source: fieldSource}}
			defaultSource := source
			if fieldSource == "Mihari" {
				defaultSource = fieldSource
			}
			annotateOverrideNode(val, field, defaultSource, origins, annotations)
			active := annotations[start]
			for j := start; j < end; j++ {
				if note, ok := annotations[j]; ok {
					active = note
				}
				line := active
				line.Text = raw[j]
				entries = append(entries, authPreviewEntry{identity: field + "\x00" + line.Text, line: line})
			}
		}
	}
	if focus != nil {
		var missing []string
		for field := range focus {
			if !seen[field] {
				missing = append(missing, field)
			}
		}
		slices.Sort(missing)
		for _, field := range missing {
			text := "# " + field + ": not set"
			entries = append(entries, authPreviewEntry{identity: text, line: protocol.ProxyAuthPreviewLine{Field: field, Text: text, Source: origins[field]}})
		}
	}
	return entries, nil
}

func annotateOverrideNode(node *yaml.Node, path, source string, origins map[string]string, notes map[int]protocol.ProxyAuthPreviewLine) {
	record := func(child *yaml.Node, key string, line int) {
		childSource := source
		if v := origins[key]; v != "" {
			childSource = v
		}
		notes[line-1] = protocol.ProxyAuthPreviewLine{Field: key, Source: childSource}
		annotateOverrideNode(child, key, source, origins, notes)
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			k, v := node.Content[i], node.Content[i+1]
			record(v, path+"."+k.Value, k.Line)
		}
	case yaml.SequenceNode:
		for i, v := range node.Content {
			record(v, path+"["+strconv.Itoa(i)+"]", v.Line)
		}
	}
}

func overrideAuthLines(field string, value *yaml.Node, reveal bool, source string, origins map[string]string) ([]authPreviewEntry, error) {
	var native any
	if err := value.Decode(&native); err != nil {
		return nil, err
	}
	lines, err := authPreviewLines(subscription.Document{field: native}, protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "follow"}, reveal, origins)
	if err != nil {
		return nil, err
	}
	var result []authPreviewEntry
	for _, entry := range lines {
		if entry.line.Field != field {
			continue
		}
		if entry.line.Source == "" {
			entry.line.Source = source
		}
		result = append(result, entry)
	}
	return result, nil
}
