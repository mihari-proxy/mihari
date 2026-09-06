package subscription

import (
	"context"
	"math"
	"net/netip"
	"strings"
)

func ruleProviderDefinitionSchema() *policySchema {
	s := objectSchema(map[string]*policySchema{
		"type":     {kind: policyString, choices: []string{"file", "http", "inline"}},
		"behavior": {kind: policyString, choices: []string{"domain", "ipcidr", "classical"}},
		"format":   {kind: policyString, choices: []string{"", "yaml", "text"}},
		"path":     stringSchema(), "url": stringSchema(), "proxy": stringSchema(),
		"interval":   integerSchema(math.MinInt64, math.MaxInt64),
		"size-limit": integerSchema(math.MinInt64, math.MaxInt64),
		"header":     policyHeadersSchema(true), "path-in-bundle": stringSchema(),
		"payload": listSchema(stringSchema()),
	})
	s.required = []string{"type", "behavior"}
	s.validate = func(v policyValue, path string) error {
		if err := validateProviderSource(v, path); err != nil {
			return err
		}
		typ, _ := v.get("type")
		bundle, _ := v.get("path-in-bundle")
		if typ.text != "inline" && bundle.text != "" {
			return policyFailure(path + ".path-in-bundle")
		}
		return nil
	}
	s.transform = func(ctx context.Context, v policyValue, path string) (policyValue, error) {
		behavior, _ := v.get("behavior")
		if payload, present := v.get("payload"); present {
			parsed, err := validateRuleProviderPayload(ctx, payload, behavior.text, path+".payload")
			if err != nil {
				return policyValue{}, err
			}
			v.set("payload", parsed)
		}
		return v, nil
	}
	return s
}

func validateRuleProviderPayload(ctx context.Context, payload policyValue, behavior, path string) (policyValue, error) {
	result := policyValue{kind: policyList, items: make([]policyValue, 0, len(payload.items))}
	for _, item := range payload.items {
		if err := ctx.Err(); err != nil {
			return policyValue{}, err
		}
		if item.text == "" {
			continue
		} // Native inline and file parsers skip empty entries.
		switch behavior {
		case "domain":
			if _, valid := policyDomainParts(item.text); !valid || strings.ContainsRune(item.text, '/') {
				return policyValue{}, policyFailure(path + "[]")
			}
			item.text = strings.ToLower(item.text)
		case "ipcidr":
			prefix, err := netip.ParsePrefix(item.text)
			if err != nil {
				return policyValue{}, policyFailure(path + "[]")
			}
			item.text = prefix.String()
		case "classical":
			rule, err := parsePolicyRule(ctx, item.text, false, path+"[]")
			if err != nil {
				return policyValue{}, err
			}
			// The native classical strategy rejects these at its outer entry.
			// A selected nested RULE-SET remains a real graph reference.
			if rule.kind == "MATCH" || rule.kind == "RULE-SET" || rule.kind == "SUB-RULE" {
				return policyValue{}, policyFailure(path + "[]")
			}
			item = policyValue{kind: policyString, text: rule.encode(), rule: rule}
		default:
			return policyValue{}, policyFailure(path)
		}
		result.items = append(result.items, item)
	}
	return result, nil
}
