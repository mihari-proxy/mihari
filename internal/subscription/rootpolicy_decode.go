package subscription

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
)

// policyValue is a private, closed value tree. It never retains source YAML.
type policyValue struct {
	kind     policyKind
	text     string
	integer  int64
	unsigned uint64
	real     float64
	boolean  bool
	items    []policyValue
	fields   []policyMember
	rule     *policyRule
	provider *policyProviderDefinition
}
type policyMember struct {
	name  string
	value policyValue
}
type policyKind uint8

const (
	policyNull policyKind = iota
	policyString
	policyBool
	policyInt
	policyUint
	policyFloat
	policyList
	policyObject
)

type policySchema struct {
	kind       policyKind
	min, max   int64
	maxUint    uint64
	choices    []string
	foldString bool
	// stringUint32 is the explicit AWG header scalar union, not generic YAML
	// weak coercion. The integer arm becomes a fresh decimal string.
	stringUint32 bool
	// scalarStringList is the declared DNS/hosts string-or-string-list union.
	// Its scalar arm is decoded through the same string element schema.
	scalarStringList bool
	reject           bool
	discriminator    string
	variants         map[string]*policySchema
	required         []string
	element          *policySchema
	fields           map[string]*policySchema
	// memberOverlays contains already typed provider overrides for declared fields.
	memberOverlays map[string]policyValue
	// prepare is reserved for a provider definition's typed override prepass.
	// It returns a compiled schema and never stores source YAML in the value tree.
	prepare func(context.Context, *yaml.Node, string) (*policySchema, error)
	// dynamic is present only for an explicitly registered data-key map.
	dynamic   *policySchema
	nullable  bool
	normalize func(string) string
	check     func(policyValue) bool
	validate  func(policyValue, string) error
	transform func(context.Context, policyValue, string) (policyValue, error)
}

func decodePolicyValue(node *yaml.Node, schema *policySchema, field string) (policyValue, error) {
	return decodePolicyValueContext(context.Background(), node, schema, field)
}

func decodePolicyValueContext(ctx context.Context, node *yaml.Node, schema *policySchema, field string) (policyValue, error) {
	if err := ctx.Err(); err != nil {
		return policyValue{}, err
	}
	fail := func() (policyValue, error) { return policyValue{}, policyFailure(field) }
	if node == nil || schema == nil || node.Anchor != "" || node.Alias != nil || node.Kind == yaml.AliasNode {
		return fail()
	}
	if schema.reject {
		return fail()
	}
	if schema.prepare != nil {
		prepared, err := schema.prepare(ctx, node, field)
		if err != nil {
			return policyValue{}, err
		}
		return decodePolicyValueContext(ctx, node, prepared, field)
	}
	if schema.variants != nil {
		path := schema.discriminator
		if field != "" {
			path = field + "." + path
		}
		if node.Kind != yaml.MappingNode || node.Tag != "!!map" || len(node.Content)%2 != 0 {
			return fail()
		}
		for i := 0; i < len(node.Content); i += 2 {
			key, typ := node.Content[i], node.Content[i+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value != schema.discriminator {
				continue
			}
			if typ.Kind != yaml.ScalarNode || typ.Tag != "!!str" || typ.Anchor != "" {
				return policyValue{}, policyFailure(path)
			}
			selected, ok := schema.variants[typ.Value]
			if !ok {
				return policyValue{}, policyFailure(path)
			}
			return decodePolicyValueContext(ctx, node, selected, field)
		}
		return policyValue{}, policyFailure(path)
	}
	if schema.nullable && node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		return policyValue{kind: policyNull}, nil
	}
	value := policyValue{kind: schema.kind}
	switch schema.kind {
	case policyString:
		if schema.stringUint32 && node.Kind == yaml.ScalarNode && node.Tag == "!!int" {
			var number uint32
			if node.Decode(&number) != nil {
				return fail()
			}
			value.text = strconv.FormatUint(uint64(number), 10)
			break
		}
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || !utf8.ValidString(node.Value) {
			return fail()
		}
		value.text = node.Value
		if schema.foldString {
			value.text = strings.ToLower(value.text)
		}
		if len(schema.choices) != 0 {
			found := false
			for _, choice := range schema.choices {
				if choice == value.text {
					found = true
					break
				}
			}
			if !found {
				return fail()
			}
		}
	case policyBool:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" || node.Decode(&value.boolean) != nil {
			return fail()
		}
	case policyInt:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" || node.Decode(&value.integer) != nil || value.integer < schema.min || value.integer > schema.max {
			return fail()
		}
	case policyUint:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" || node.Decode(&value.unsigned) != nil || value.unsigned > schema.maxUint {
			return fail()
		}
	case policyFloat:
		if node.Kind != yaml.ScalarNode || (node.Tag != "!!float" && node.Tag != "!!int") || node.Decode(&value.real) != nil || math.IsNaN(value.real) || math.IsInf(value.real, 0) {
			return fail()
		}
	case policyList:
		if schema.scalarStringList && node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
			item, err := decodePolicyValueContext(ctx, node, schema.element, field+"[]")
			if err != nil {
				return policyValue{}, err
			}
			value.items = []policyValue{item}
			break
		}
		if node.Kind != yaml.SequenceNode || node.Tag != "!!seq" {
			return fail()
		}
		value.items = make([]policyValue, 0, len(node.Content))
		for _, child := range node.Content {
			item, err := decodePolicyValueContext(ctx, child, schema.element, field+"[]")
			if err != nil {
				return policyValue{}, err
			}
			value.items = append(value.items, item)
		}
	case policyObject:
		if node.Kind != yaml.MappingNode || node.Tag != "!!map" || len(node.Content)%2 != 0 {
			return fail()
		}
		seen := make(map[string]bool, len(node.Content)/2)
		value.fields = make([]policyMember, 0, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Anchor != "" || key.Alias != nil || !utf8.ValidString(key.Value) {
				return fail()
			}
			name := key.Value
			if schema.normalize != nil {
				name = schema.normalize(name)
			}
			childSchema, registered := schema.fields[name]
			childPath := name
			if field != "" {
				childPath = field + "." + name
			}
			if !registered {
				childSchema = schema.dynamic
				childPath = field + ".[entry]"
				if childSchema == nil {
					return policyValue{}, policyFailure(field + ".[unknown]")
				}
			}
			if seen[name] {
				return policyValue{}, policyFailure(childPath)
			}
			seen[name] = true
			childNode := node.Content[i+1]
			if overlay, ok := schema.memberOverlays[name]; ok && registered {
				childNode = overlay.yamlNode()
			}
			child, err := decodePolicyValueContext(ctx, childNode, childSchema, childPath)
			if err != nil {
				return policyValue{}, err
			}
			value.fields = append(value.fields, policyMember{name: name, value: child})
		}
		// Missing declared fields may be supplied by an override. Sort additions so
		// map iteration cannot change generated bytes or resource digests.
		additions := make([]string, 0, len(schema.memberOverlays))
		for name := range schema.memberOverlays {
			if _, registered := schema.fields[name]; registered && !seen[name] {
				additions = append(additions, name)
			}
		}
		sort.Strings(additions)
		for _, name := range additions {
			path := name
			if field != "" {
				path = field + "." + name
			}
			child, err := decodePolicyValueContext(ctx, schema.memberOverlays[name].yamlNode(), schema.fields[name], path)
			if err != nil {
				return policyValue{}, err
			}
			seen[name] = true
			value.fields = append(value.fields, policyMember{name: name, value: child})
		}
		for _, name := range schema.required {
			if !seen[name] {
				path := name
				if field != "" {
					path = field + "." + name
				}
				return policyValue{}, policyFailure(path)
			}
		}
	default:
		return fail()
	}
	if schema.check != nil && !schema.check(value) {
		return fail()
	}
	if schema.validate != nil {
		if err := schema.validate(value, field); err != nil {
			return policyValue{}, err
		}
	}
	if schema.transform != nil {
		return schema.transform(ctx, value, field)
	}
	return value, nil
}

func policyFailure(field string) error {
	return PolicyError{Field: field, Code: protocol.CodeDataFailure}
}

// yamlNode builds a fresh canonical syntax tree, including every map key.
func (v policyValue) yamlNode() *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode}
	switch v.kind {
	case policyNull:
		n.Tag, n.Value = "!!null", "null"
	case policyString:
		n.Tag, n.Value = "!!str", v.text
	case policyBool:
		n.Tag, n.Value = "!!bool", strconv.FormatBool(v.boolean)
	case policyInt:
		n.Tag, n.Value = "!!int", strconv.FormatInt(v.integer, 10)
	case policyUint:
		n.Tag, n.Value = "!!int", strconv.FormatUint(v.unsigned, 10)
	case policyFloat:
		n.Tag, n.Value = "!!float", strconv.FormatFloat(v.real, 'g', -1, 64)
	case policyList:
		n.Kind, n.Tag = yaml.SequenceNode, "!!seq"
		for _, item := range v.items {
			n.Content = append(n.Content, item.yamlNode())
		}
	case policyObject:
		n.Kind, n.Tag = yaml.MappingNode, "!!map"
		for _, member := range v.fields {
			n.Content = append(n.Content, (&policyValue{kind: policyString, text: member.name}).yamlNode(), member.value.yamlNode())
		}
	}
	return n
}

func (v policyValue) get(name string) (policyValue, bool) {
	for _, member := range v.fields {
		if member.name == name {
			return member.value, true
		}
	}
	return policyValue{}, false
}

func (v *policyValue) set(name string, value policyValue) {
	for i := range v.fields {
		if v.fields[i].name == name {
			v.fields[i].value = value
			return
		}
	}
	v.fields = append(v.fields, policyMember{name: name, value: value})
}

func (v *policyValue) remove(name string) {
	for i := range v.fields {
		if v.fields[i].name == name {
			v.fields = append(v.fields[:i], v.fields[i+1:]...)
			return
		}
	}
}
