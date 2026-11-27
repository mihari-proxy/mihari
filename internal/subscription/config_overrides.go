package subscription

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"go.yaml.in/yaml/v3"
)

// ApplyConfigOverrides applies enabled fragments and the DNS form to a detached
// subscription. Source paths use dot-separated keys and zero-based [indices].
// Absent sources belong to the original subscription.
func ApplyConfigOverrides(document Document, overrides *protocol.ConfigOverrides) (map[string]string, error) {
	if overrides == nil {
		return nil, nil
	}
	root := fieldOrigin{}
	for _, fragment := range overrides.Fragments {
		if !fragment.Enabled {
			continue
		}
		node, err := parseOverrideFragment(fragment.YAML)
		if err == nil {
			_, err = mergeOverrideMapping(document, &root, node, "", fragment.Name)
		}
		if err != nil {
			return nil, overrideError(fmt.Errorf("fragment %q: %w", fragment.Name, err))
		}
	}
	if err := applyDNSForm(document, &root, overrides.DNS); err != nil {
		return nil, overrideError(err)
	}
	if dns := root.fields["dns"]; dns != nil && dns.source != "" {
		if err := validateEffectiveDNS(document); err != nil {
			return nil, overrideError(err)
		}
	}
	sources := make(map[string]string)
	root.collect("", sources)
	return sources, nil
}

func overrideError(err error) error {
	return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInvalidArgument, Message: err.Error()}, err)
}

// fieldOrigin mirrors only modified subtrees. List operations move the origins
// with the values, so inherited users and rules keep their original source.
type fieldOrigin struct {
	source string
	fields map[string]*fieldOrigin
	items  []*fieldOrigin
}

func (o *fieldOrigin) field(key string) *fieldOrigin {
	if o.fields == nil {
		o.fields = make(map[string]*fieldOrigin)
	}
	if o.fields[key] == nil {
		o.fields[key] = &fieldOrigin{}
	}
	return o.fields[key]
}

func (o *fieldOrigin) collect(path string, result map[string]string) {
	if o == nil {
		return
	}
	if path != "" && o.source != "" {
		result[path] = o.source
	}
	for k, child := range o.fields {
		child.collect(overridePath(path, k), result)
	}
	for index, child := range o.items {
		child.collect(path+"["+strconv.Itoa(index)+"]", result)
	}
}

func (o *fieldOrigin) replace(value any, source string) {
	*o = fieldOrigin{source: source}
	for k, v := range egressMapping(value) {
		o.field(k).replace(v, source)
	}
	if list, ok := value.([]any); ok {
		o.items = itemOrigins(list, source)
	}
}

func itemOrigins(list []any, source string) []*fieldOrigin {
	items := make([]*fieldOrigin, len(list))
	for i, value := range list {
		items[i] = &fieldOrigin{}
		items[i].replace(value, source)
	}
	return items
}

func overridePath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func parseOverrideFragment(raw string) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("read YAML mapping: %w", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("expected one YAML mapping")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("read YAML: %w", err)
		}
		return nil, errors.New("expected one YAML document")
	}
	// Decode once before traversing nodes: yaml.v3 checks duplicate keys,
	// cyclic aliases and excessive alias expansion. Traversal retains key order.
	var checked map[string]any
	if err := doc.Decode(&checked); err != nil {
		return nil, err
	}
	return doc.Content[0], nil
}

type fieldOperation uint8

const (
	mergeField fieldOperation = iota
	replaceField
	prependItems
	appendItems
	removeFieldOrItems
)

func overrideKey(raw string) (string, fieldOperation, error) {
	op := mergeField
	switch {
	case strings.HasPrefix(raw, "+"):
		raw, op = raw[1:], prependItems
	case strings.HasSuffix(raw, "!"):
		raw, op = raw[:len(raw)-1], replaceField
	case strings.HasSuffix(raw, "+"):
		raw, op = raw[:len(raw)-1], appendItems
	case strings.HasSuffix(raw, "-"):
		raw, op = raw[:len(raw)-1], removeFieldOrItems
	}
	if strings.HasPrefix(raw, "<") && strings.HasSuffix(raw, ">") {
		raw = raw[1 : len(raw)-1]
	}
	if raw == "" {
		return "", op, errors.New("empty override target key")
	}
	return raw, op, nil
}

func protectedOverride(key string) bool {
	switch key {
	case "mode", "log-level", "mixed-port", "bind-address", "allow-lan", "external-controller", "secret", "external-ui", "external-ui-name", "external-ui-url":
		return true
	}
	return false
}

// overrideEntries expands YAML merge aliases without decoding into an unordered
// Go map. Explicit raw keys take precedence over inherited keys. In a merge
// sequence, earlier mappings contribute first.
func overrideEntries(mapping *yaml.Node) []*yaml.Node {
	content := mapping.Content
	mergeAt := -1
	for i := 0; i < len(content); i += 2 {
		if content[i].ShortTag() == "!!merge" {
			mergeAt = i
			break
		}
	}
	if mergeAt < 0 {
		return content
	}
	claimed := make(map[string]bool, len(content)/2)
	for i := 0; i < len(content); i += 2 {
		if i != mergeAt {
			claimed[content[i].Value] = true
		}
	}
	result := append([]*yaml.Node(nil), content[:mergeAt]...)
	merge := content[mergeAt+1]
	if merge.Kind == yaml.AliasNode {
		merge = merge.Alias
	}
	parents := []*yaml.Node{merge}
	if merge.Kind == yaml.SequenceNode {
		parents = merge.Content
	}
	for _, parent := range parents {
		if parent.Kind == yaml.AliasNode {
			parent = parent.Alias
		}
		entries := overrideEntries(parent)
		for i := 0; i < len(entries); i += 2 {
			key := entries[i].Value
			if !claimed[key] {
				result = append(result, entries[i], entries[i+1])
				claimed[key] = true
			}
		}
	}
	return append(result, content[mergeAt+2:]...)
}

func mergeOverrideMapping(target map[string]any, origins *fieldOrigin, mapping *yaml.Node, parent, source string) (bool, error) {
	applied := false
	entries := overrideEntries(mapping)
	for i := 0; i < len(entries); i += 2 {
		rawKey, value := entries[i], entries[i+1]
		if rawKey.Kind != yaml.ScalarNode || rawKey.ShortTag() != "!!str" {
			return false, fmt.Errorf("line %d: override key must be a string", rawKey.Line)
		}
		key, operation, err := overrideKey(rawKey.Value)
		if err != nil {
			return false, fmt.Errorf("line %d: %w", rawKey.Line, err)
		}
		path := overridePath(parent, key)
		if parent == "" && protectedOverride(key) {
			return false, fmt.Errorf("%s is managed by Mihari", key)
		}
		if value.Kind == yaml.AliasNode {
			value = value.Alias
		}
		if (operation == prependItems || operation == appendItems || operation == removeFieldOrItems) && value.Kind == yaml.SequenceNode && len(value.Content) == 0 {
			continue
		}
		field := origins.field(key)
		if operation == mergeField && value.Kind == yaml.MappingNode {
			existing, exists := target[key]
			dest := egressMapping(existing)
			childOrigin := field
			if dest == nil {
				dest = map[string]any{}
				childOrigin = &fieldOrigin{}
			}
			childApplied, err := mergeOverrideMapping(dest, childOrigin, value, path, source)
			if err != nil {
				return false, err
			}
			if len(value.Content) > 0 && (!childApplied || !exists && len(dest) == 0) {
				continue
			}
			target[key] = dest
			childOrigin.source = source
			if childOrigin != field {
				*field = *childOrigin
			}
			applied = true
			continue
		}
		var decoded any
		if err := value.Decode(&decoded); err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
		if err := applyFieldOperation(target, field, key, operation, decoded, source); err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
		applied = true
	}
	return applied, nil
}

func applyFieldOperation(target map[string]any, origin *fieldOrigin, key string, op fieldOperation, value any, source string) error {
	if op == mergeField || op == replaceField {
		target[key] = value
		origin.replace(value, source)
		return nil
	}
	if op == removeFieldOrItems && value == nil {
		delete(target, key)
		origin.replace(nil, source)
		return nil
	}
	values, ok := value.([]any)
	if !ok {
		return errors.New("list operation requires a list (use null to delete the field)")
	}
	current, exists := target[key]
	list, ok := current.([]any)
	if exists && !ok {
		return errors.New("list operation requires an existing list or an absent key")
	}
	if op == removeFieldOrItems && (!exists || len(values) == 0) {
		return nil
	}
	items := origin.items
	if items == nil {
		items = make([]*fieldOrigin, len(list))
	}
	switch op {
	case prependItems:
		list = append(values, list...)
		items = append(itemOrigins(values, source), items...)
	case appendItems:
		list = append(list, values...)
		items = append(items, itemOrigins(values, source)...)
	case removeFieldOrItems:
		kept := make([]any, 0, len(list))
		keptOrigins := make([]*fieldOrigin, 0, len(list))
		removed := newRemovalSet(values)
		for i, item := range list {
			if !removed.contains(item) {
				kept = append(kept, item)
				keptOrigins = append(keptOrigins, items[i])
			}
		}
		list, items = kept, keptOrigins
	}
	target[key] = list
	*origin = fieldOrigin{source: source, items: items}
	return nil
}

// DNS endpoints and rules are strings. Index scalar removals so filtering long
// subscription lists stays linear in their size.
type removalSet struct {
	scalars     map[any]bool
	collections []any
}

func newRemovalSet(values []any) removalSet {
	result := removalSet{scalars: make(map[any]bool)}
	for _, v := range values {
		if v == nil || reflect.TypeOf(v).Comparable() {
			result.scalars[v] = true
		} else {
			result.collections = append(result.collections, v)
		}
	}
	return result
}

func (set removalSet) contains(item any) bool {
	if item == nil || reflect.TypeOf(item).Comparable() {
		return set.scalars[item]
	}
	for _, v := range set.collections {
		if equalOverrideValue(item, v) {
			return true
		}
	}
	return false
}

// Nested subscription mappings can be Document; fragment values decode to
// map[string]any. YAML equality depends on their contents, not the Go map type.
func equalOverrideValue(a, b any) bool {
	if left := egressMapping(a); left != nil {
		right := egressMapping(b)
		if right == nil || len(left) != len(right) {
			return false
		}
		for key, v := range left {
			other, ok := right[key]
			if !ok || !equalOverrideValue(v, other) {
				return false
			}
		}
		return true
	}
	if left, ok := a.([]any); ok {
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for i, v := range left {
			if !equalOverrideValue(v, right[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

// FragmentFields returns the distinct top-level configuration keys addressed by
// a fragment, including removals, in first-appearance order.
func FragmentFields(fragment protocol.OverrideFragment) ([]string, error) {
	node, err := parseOverrideFragment(fragment.YAML)
	if err != nil {
		return nil, err
	}
	var fields []string
	seen := make(map[string]bool)
	entries := overrideEntries(node)
	for i := 0; i < len(entries); i += 2 {
		k := entries[i]
		if k.Kind != yaml.ScalarNode || k.ShortTag() != "!!str" {
			return nil, fmt.Errorf("line %d: override key must be a string", k.Line)
		}
		key, _, err := overrideKey(k.Value)
		if err != nil {
			return nil, err
		}
		if !seen[key] {
			fields = append(fields, key)
			seen[key] = true
		}
	}
	return fields, nil
}
