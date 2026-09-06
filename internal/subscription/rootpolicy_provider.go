package subscription

import (
	"context"
	"math"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// policyProviderDefinition retains only decoded overlay data for separately
// prepared resource bytes. Neither it nor generated YAML retains a source node.
type policyProviderDefinition struct {
	override policyValue
	dialer   string
}

func proxyProviderDefinitionSchema() *policySchema {
	schema := objectSchema(nil)
	schema.prepare = prepareProxyProviderDefinition
	return schema
}

func prepareProxyProviderDefinition(ctx context.Context, node *yaml.Node, path string) (*policySchema, error) {
	if node.Kind != yaml.MappingNode || node.Tag != "!!map" || len(node.Content)%2 != 0 {
		return nil, policyFailure(path)
	}
	override := policyValue{kind: policyObject}
	dialer := ""
	seen := make(map[string]bool)
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || (key.Value != "override" && key.Value != "dialer-proxy") {
			continue
		}
		if seen[key.Value] {
			return nil, policyFailure(path + "." + key.Value)
		}
		seen[key.Value] = true
		childSchema := stringSchema()
		if key.Value == "override" {
			childSchema = providerOverrideSchema()
		}
		value, err := decodePolicyValueContext(ctx, node.Content[i+1], childSchema, path+"."+key.Value)
		if err != nil {
			return nil, err
		}
		if key.Value == "override" {
			override = value
		} else {
			dialer = value.text
		}
	}
	// The normal object decoder still checks every original key and duplicate.
	// Only the declared proxy payload leaves receive the prepared typed overlay.
	schema := objectSchema(map[string]*policySchema{
		"type":    &policySchema{kind: policyString, choices: []string{"inline", "file", "http"}},
		"payload": listSchema(providerPayloadProxySchema(override, dialer)),
		"filter":  stringSchema(), "exclude-filter": stringSchema(), "exclude-type": stringSchema(),
		"dialer-proxy": stringSchema(), "override": providerOverrideSchema(),
		"path": stringSchema(), "url": stringSchema(), "proxy": stringSchema(),
		"interval":   integerSchema(math.MinInt64, math.MaxInt64),
		"size-limit": integerSchema(math.MinInt64, math.MaxInt64),
		"header":     policyHeadersSchema(true), "health-check": providerHealthSchema(),
		"age-secret-key": stringSchema(),
	})
	schema.required = []string{"type"}
	schema.validate = func(v policyValue, path string) error {
		if err := validateProviderSource(v, path); err != nil {
			return err
		}
		if age, _ := v.get("age-secret-key"); age.text != "" {
			return policyFailure(path + ".age-secret-key")
		}
		return nil
	}
	schema.transform = func(_ context.Context, value policyValue, _ string) (policyValue, error) {
		value.provider = &policyProviderDefinition{override: override, dialer: dialer}
		value.remove("dialer-proxy")
		if retained, exists := value.get("override"); exists {
			for _, name := range providerSimpleOverrideNames {
				retained.remove(name)
			}
			value.set("override", retained)
		}
		return value, nil
	}
	return schema, nil
}

func providerHealthSchema() *policySchema {
	status := stringSchema()
	status.check = func(v policyValue) bool { return validUnsignedRanges(v.text, math.MaxUint16, 28) }
	s := objectSchema(map[string]*policySchema{
		"enable": boolSchema(), "url": policyHTTPURLSchema(), "lazy": boolSchema(),
		"interval":        integerSchema(math.MinInt64, math.MaxInt64),
		"timeout":         integerSchema(-math.MaxInt64/int64(time.Millisecond), math.MaxInt64/int64(time.Millisecond)),
		"expected-status": status,
	})
	s.required = []string{"enable"}
	s.nullable = true
	s.validate = func(v policyValue, path string) error {
		enable, _ := v.get("enable")
		endpoint, _ := v.get("url")
		interval, _ := v.get("interval")
		// Empty URL disables the native ticker even when enable is true. Group
		// tasks can still use timeout, so timeout's bound is never dormant.
		if enable.boolean && endpoint.text != "" && (interval.integer < 0 || interval.integer > math.MaxInt64/int64(time.Second)) {
			return policyFailure(path + ".interval")
		}
		return nil
	}
	return s
}

func validProviderResourceID(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validProviderCacheSuggestion(value string) bool {
	value = strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(value, "/") || strings.ContainsAny(value, ":\x00") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func validateProviderSource(v policyValue, path string) error {
	typ, _ := v.get("type")
	source, _ := v.get("path")
	if !validProviderCacheSuggestion(source.text) {
		return policyFailure(path + ".path")
	}
	if typ.text == "file" && !validProviderResourceID(source.text) {
		return policyFailure(path + ".path")
	}
	if typ.text == "http" {
		endpoint, _ := v.get("url")
		if endpoint.text == "" || !policyHTTPURLSchema().check(endpoint) {
			return policyFailure(path + ".url")
		}
		proxy, _ := v.get("proxy")
		if proxy.text != "" {
			return policyFailure(path + ".proxy")
		}
		if interval, present := v.get("interval"); present && (interval.integer < 60 || interval.integer > math.MaxInt64/int64(time.Second)) {
			return policyFailure(path + ".interval")
		}
	}
	return nil
}
