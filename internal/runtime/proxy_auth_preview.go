package runtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"go.yaml.in/yaml/v3"
)

// PreviewProxyAuth resolves authentication YAML without saving, validating with
// the core, or reloading it. Both sides of the diff use the same source snapshot.
func (m *Manager) PreviewProxyAuth(ctx context.Context, request protocol.ProxyAuthPreviewRequest) (protocol.ProxyAuthPreview, error) {
	if err := m.lockMaintenance(ctx); err != nil {
		return protocol.ProxyAuthPreview{}, err
	}
	settings, catalog := m.settingsSnapshot(), m.loggingCatalog()
	generation, revision := m.currentConfigGeneration(), m.store.Load().Revision
	m.unlock()
	base := subscription.Document{}
	if catalog.ActiveID != "" {
		_, document, err := m.subscriptions.ReadCache(catalog.ActiveID)
		if err != nil {
			return protocol.ProxyAuthPreview{}, fmt.Errorf("read authentication preview source: %w", err)
		}
		base = document
	}
	if err := ctx.Err(); err != nil {
		return protocol.ProxyAuthPreview{}, err
	}
	old := settings.EffectiveProxyAuth()
	draft := resolveProxyAuthPasswords(request.Config, old)
	if err := config.ValidateProxyAuth(draft); err != nil {
		return protocol.ProxyAuthPreview{}, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInvalidArgument, Message: err.Error()}, err)
	}
	before, err := authPreviewLines(base, old, request.Reveal)
	if err != nil {
		return protocol.ProxyAuthPreview{}, err
	}
	after, err := authPreviewLines(base, draft, request.Reveal)
	if err != nil {
		if errors.Is(err, subscription.ErrProxyAuthNoUsers) {
			return protocol.ProxyAuthPreview{}, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInvalidArgument, Message: err.Error()}, err)
		}
		return protocol.ProxyAuthPreview{}, err
	}
	if err := m.lockMaintenance(ctx); err != nil {
		return protocol.ProxyAuthPreview{}, err
	}
	stale := generation != m.currentConfigGeneration() || revision != m.store.Load().Revision || !reflect.DeepEqual(catalog, m.loggingCatalog())
	m.unlock()
	if stale {
		return protocol.ProxyAuthPreview{}, routingConflict()
	}
	return protocol.ProxyAuthPreview{Revision: revision, ActiveID: catalog.ActiveID, Lines: diffAuthPreview(before, after)}, nil
}

func resolveProxyAuthPasswords(draft, saved protocol.ProxyAuthConfig) protocol.ProxyAuthConfig {
	draft.Users = append([]protocol.ProxyAuthUser{}, draft.Users...)
	for i := range draft.Users {
		draft.Users[i].Source = ""
		if draft.Users[i].Password == "" {
			for _, old := range saved.Users {
				if old.Username == draft.Users[i].Username {
					draft.Users[i].Password = old.Password
					break
				}
			}
		}
	}
	return draft
}

type authPreviewEntry struct {
	identity string
	line     protocol.ProxyAuthPreviewLine
}

func authPreviewLines(base subscription.Document, auth protocol.ProxyAuthConfig, reveal bool) ([]authPreviewEntry, error) {
	document := subscription.Document{}
	for key, value := range base {
		document[key] = value
	}
	if _, err := subscription.ApplyProxyAuth(document, auth); err != nil {
		return nil, err
	}
	var result []authPreviewEntry
	for _, field := range []string{"authentication", "skip-auth-prefixes"} {
		value, exists := document[field]
		if !exists {
			text := "# " + field + ": not set"
			result = append(result, authPreviewEntry{identity: text, line: protocol.ProxyAuthPreviewLine{Text: text, Field: field}})
			continue
		}
		var node yaml.Node
		if err := node.Encode(value); err != nil {
			return nil, fmt.Errorf("encode authentication preview: %w", err)
		}
		if node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
			raw, err := yaml.Marshal(subscription.Document{field: value})
			if err != nil {
				return nil, fmt.Errorf("render authentication preview: %w", err)
			}
			for _, part := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
				text := part
				result = append(result, authPreviewEntry{identity: text, line: protocol.ProxyAuthPreviewLine{Text: text, Field: field}})
			}
			continue
		}
		result = append(result, authPreviewEntry{identity: field + ":", line: protocol.ProxyAuthPreviewLine{Text: field + ":", Field: field}})
		for _, item := range node.Content {
			// Quote scalar strings so credentials and control characters remain a
			// single YAML line; the UI additionally escapes terminal controls.
			if item.Kind == yaml.ScalarNode && item.Tag == "!!str" {
				item.Style = yaml.DoubleQuotedStyle
			}
			raw, err := yaml.Marshal(item)
			if err != nil {
				return nil, fmt.Errorf("render authentication preview entry: %w", err)
			}
			target := item.Value
			if field == "authentication" && item.Kind == yaml.ScalarNode && item.Tag == "!!str" {
				name, _, found := strings.Cut(item.Value, ":")
				target = name
				if !reveal && found {
					item.Value = name + ":********"
				}
			}
			shown, err := yaml.Marshal(item)
			if err != nil {
				return nil, fmt.Errorf("render masked authentication preview: %w", err)
			}
			identity := field + ":" + string(raw)
			for i, part := range strings.Split(strings.TrimSuffix(string(shown), "\n"), "\n") {
				prefix := "    "
				if i == 0 {
					prefix = "  - "
				}
				result = append(result, authPreviewEntry{identity: fmt.Sprintf("%s:%d", identity, i), line: protocol.ProxyAuthPreviewLine{Text: prefix + part, Field: field, Target: target}})
			}
		}
	}
	return result, nil
}

// Diff using ordered identity queues avoids quadratic memory for large native
// subscription lists. Compare before masking, including password-only changes.
func diffAuthPreview(before, after []authPreviewEntry) []protocol.ProxyAuthPreviewLine {
	positions := map[string][]int{}
	for i, entry := range before {
		positions[entry.identity] = append(positions[entry.identity], i)
	}
	result := []protocol.ProxyAuthPreviewLine{}
	add := func(entry authPreviewEntry, change string) {
		line := entry.line
		line.Change = change
		result = append(result, line)
	}
	cursor := 0
	for _, entry := range after {
		queue := positions[entry.identity]
		for len(queue) > 0 && queue[0] < cursor {
			queue = queue[1:]
		}
		positions[entry.identity] = queue
		if len(queue) == 0 {
			add(entry, "+")
			continue
		}
		match := queue[0]
		positions[entry.identity] = queue[1:]
		for cursor < match {
			add(before[cursor], "-")
			cursor++
		}
		add(entry, "")
		cursor++
	}
	for cursor < len(before) {
		add(before[cursor], "-")
		cursor++
	}
	return result
}
